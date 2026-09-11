// Command api runs the Central Station HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	v1 "central-station/internal/api/v1"
	"central-station/internal/auth"
	"central-station/internal/config"
	"central-station/internal/db"
	"central-station/internal/fallback"
	"central-station/internal/player"
	"central-station/internal/provider"
	"central-station/internal/provider/youtube"
	"central-station/internal/queue"
	"central-station/internal/rbac"
	"central-station/internal/server"
	"central-station/internal/settings"
	"central-station/internal/store"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// originPatterns extracts host[:port] patterns for WebSocket origin checks.
func originPatterns(urls ...string) []string {
	var out []string
	for _, raw := range urls {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			out = append(out, u.Host)
		}
	}
	return out
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	level := slog.LevelDebug
	if cfg.IsProduction() {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings {
		logger.Warn(w)
	}
	if err := rbac.Configure(cfg.RolePermissions); err != nil {
		return fmt.Errorf("ROLE_PERMISSIONS: %w", err)
	}
	if len(cfg.RolePermissions) > 0 {
		logger.Info("role permissions overridden", "roles", len(cfg.RolePermissions))
	}

	registry, err := provider.NewRegistry(cfg.DefaultProvider, youtube.New(cfg.YouTubeAPIKey))
	if err != nil {
		return err
	}

	var google *auth.Google
	if cfg.GoogleConfigured() {
		google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		users         store.UserStore
		queueStore    queue.Store
		settingsStore settings.Store
		ready         = func() bool { return true }
	)
	if cfg.DatabaseURL != "" {
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		if err := db.Migrate(ctx, pool); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		users = store.NewPostgres(pool)
		queueStore = queue.NewPostgresStore(pool)
		settingsStore = settings.NewPostgres(pool)
		ready = func() bool {
			pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return pool.Ping(pctx) == nil
		}
		logger.Info("database connected")
	} else {
		users = store.NewMemory()
		queueStore = queue.NewMemoryStore()
		settingsStore = settings.NewMemory()
	}

	q, err := queue.New(ctx, queueStore)
	if err != nil {
		return err
	}
	hub := player.NewHub(q, logger)
	hub.OriginPatterns = originPatterns(cfg.FrontendURL, cfg.BaseURL)
	hub.VolumeStore = settingsStore
	if err := hub.LoadVolume(ctx); err != nil {
		logger.Warn("load volume; using default", "err", err)
	}

	// Default playlist: plays whenever the queue is empty.
	fb, err := fallback.New(ctx, settingsStore, registry, logger)
	if err != nil {
		return err
	}
	fb.OnChange = q.Notify
	if cfg.FallbackPlaylistURL != "" && !fb.Configured() {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, seeded, err := fb.Seed(sctx, fallback.Settings{URL: cfg.FallbackPlaylistURL, Enabled: true, Shuffle: cfg.FallbackShuffle})
		cancel()
		if err != nil {
			logger.Warn("FALLBACK_PLAYLIST_URL could not be loaded; set the default playlist from the UI", "url", cfg.FallbackPlaylistURL, "err", err)
		} else if seeded {
			logger.Info("default playlist seeded from FALLBACK_PLAYLIST_URL", "url", cfg.FallbackPlaylistURL)
		}
	}
	q.SetFallback(fb)
	if err := q.Nudge(ctx); err != nil {
		logger.Warn("start default playlist", "err", err)
	}

	deps := v1.Deps{
		Config:    cfg,
		Users:     users,
		Queue:     q,
		Hub:       hub,
		Fallback:  fb,
		Providers: registry,
		Sessions:  auth.NewSessions(cfg.SessionSecret, cfg.SessionTTL, cfg.CookieSecure),
		Google:    google,
	}

	handler := server.New(server.Options{
		Logger:         logger,
		AllowedOrigins: []string{cfg.FrontendURL},
		Versions:       map[string]http.Handler{"v1": v1.Handler(deps)},
		Ready:          ready,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "version", version, "addr", cfg.Addr, "env", cfg.Env, "baseUrl", cfg.BaseURL, "frontendUrl", cfg.FrontendURL,
			"googleSSO", cfg.GoogleConfigured(), "devMode", cfg.AuthDevMode, "defaultProvider", registry.DefaultID())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
