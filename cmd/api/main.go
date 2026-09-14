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

	"central-station/internal/alerts"
	"central-station/internal/announce"
	v1 "central-station/internal/api/v1"
	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/config"
	"central-station/internal/db"
	"central-station/internal/fallback"
	"central-station/internal/mail"
	"central-station/internal/provider"
	"central-station/internal/provider/youtube"
	"central-station/internal/queue"
	"central-station/internal/rbac"
	"central-station/internal/server"
	"central-station/internal/settings"
	"central-station/internal/store"
	"central-station/internal/tts"
	"central-station/internal/zone"
	"central-station/internal/zones"
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
// originPatterns turns browser origins into WebSocket origin patterns
// (host[:port]); "*" is passed through and matches every host.
func originPatterns(urls ...string) []string {
	var out []string
	for _, raw := range urls {
		if raw == config.AnyOrigin {
			out = append(out, raw)
			continue
		}
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
	if cfg.GoogleEnabled() {
		google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		users         store.UserStore
		newQueueStore func(zoneID string) queue.Store
		zoneStore     zone.Store
		settingsStore settings.Store
		clipStore     clips.Store
		alertStore    alerts.Store
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
		newQueueStore = func(zoneID string) queue.Store { return queue.NewPostgresStore(pool, zoneID) }
		zoneStore = zone.NewPostgres(pool)
		settingsStore = settings.NewPostgres(pool)
		clipStore = clips.NewPostgres(pool)
		alertStore = alerts.NewPostgres(pool)
		ready = func() bool {
			pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return pool.Ping(pctx) == nil
		}
		logger.Info("database connected")
	} else {
		users = store.NewMemory()
		newQueueStore = func(string) queue.Store { return queue.NewMemoryStore() }
		zoneStore = zone.NewMemory()
		settingsStore = settings.NewMemory()
		clipStore = clips.NewMemory()
		alertStore = alerts.NewMemory()
	}
	if err := clips.EnsureBuiltins(ctx, clipStore); err != nil {
		return fmt.Errorf("install bundled chimes: %w", err)
	}
	ttsClient := tts.New(cfg.TTSURL)

	// One queue + player hub + default playlist per zone.
	zm, err := zones.New(ctx, zones.Deps{
		Store:          zoneStore,
		Settings:       settingsStore,
		Resolver:       registry,
		Logger:         logger,
		NewQueueStore:  newQueueStore,
		OriginPatterns: originPatterns(cfg.BrowserOrigins()...),
		LegacyToken:    cfg.PlayerToken,
	})
	if err != nil {
		return err
	}
	logger.Info("zones loaded", "count", len(zm.List()))

	// FALLBACK_PLAYLIST_URL seeds the default zone's playlist once.
	if fb := zm.Default().Fallback; cfg.FallbackPlaylistURL != "" && !fb.Configured() {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, seeded, err := fb.Seed(sctx, fallback.Settings{URL: cfg.FallbackPlaylistURL, Enabled: true, Shuffle: cfg.FallbackShuffle})
		cancel()
		if err != nil {
			logger.Warn("FALLBACK_PLAYLIST_URL could not be loaded; set the default playlist from the UI", "url", cfg.FallbackPlaylistURL, "err", err)
		} else if seeded {
			logger.Info("default playlist seeded from FALLBACK_PLAYLIST_URL", "url", cfg.FallbackPlaylistURL)
		}
	}
	zm.Nudge(ctx)

	// Scheduled announcements fire through the same path as manual broadcasts.
	sched := &alerts.Scheduler{Store: alertStore, Logger: logger, Fire: func(ctx context.Context, al *alerts.Alert) error {
		b, err := announce.Build(ctx, clipStore, announce.DefaultChime(ctx, settingsStore), al.ClipID, al.Loops, al.ChimeID, "alert", "Alert: "+al.Name)
		if err != nil {
			return err
		}
		_, err = zm.SendBroadcast(al.ZoneID, b)
		return err
	}}
	go sched.Run(ctx)

	deps := v1.Deps{
		Config:    cfg,
		Users:     users,
		Zones:     zm,
		Clips:     clipStore,
		TTS:       ttsClient,
		Settings:  settingsStore,
		Alerts:    alertStore,
		Mail:      &mail.Client{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom, StartTLS: cfg.SMTPStartTLS},
		Providers: registry,
		Sessions:  auth.NewSessions(cfg.SessionSecret, cfg.SessionTTL, cfg.CookieSecure),
		Google:    google,
	}

	handler := server.New(server.Options{
		Logger:         logger,
		AllowedOrigins: cfg.BrowserOrigins(),
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
			"googleSSO", cfg.GoogleEnabled(), "passwordSignIn", cfg.AuthPasswordEnabled, "demoMode", cfg.AuthDemoMode, "smtp", cfg.SMTPHost != "", "devMode", cfg.AuthDevMode, "defaultProvider", registry.DefaultID(), "tts", ttsClient.Enabled())
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
