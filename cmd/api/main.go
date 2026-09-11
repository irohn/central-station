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
	"central-station/internal/player"
	"central-station/internal/provider"
	"central-station/internal/provider/youtube"
	"central-station/internal/queue"
	"central-station/internal/server"
	"central-station/internal/store"
)

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
		users      store.UserStore
		queueStore queue.Store
		ready      = func() bool { return true }
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
		ready = func() bool {
			pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return pool.Ping(pctx) == nil
		}
		logger.Info("database connected")
	} else {
		users = store.NewMemory()
		queueStore = queue.NewMemoryStore()
	}

	q, err := queue.New(ctx, queueStore)
	if err != nil {
		return err
	}
	hub := player.NewHub(q, logger)
	hub.OriginPatterns = originPatterns(cfg.FrontendURL, cfg.BaseURL)

	deps := v1.Deps{
		Config:    cfg,
		Users:     users,
		Queue:     q,
		Hub:       hub,
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
		logger.Info("api listening", "addr", cfg.Addr, "env", cfg.Env, "baseUrl", cfg.BaseURL, "frontendUrl", cfg.FrontendURL,
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
