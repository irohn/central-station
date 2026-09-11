// Package config loads runtime configuration from environment variables.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Env         string // development | production
	Addr        string // listen address, e.g. ":8080"
	BaseURL     string // public URL of this API
	FrontendURL string // origin of the web app (CORS + post-login redirect)

	SessionSecret string
	SessionTTL    time.Duration
	CookieSecure  bool

	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string

	// Sign-in allow-list. When AccessAllowlist is true only ADMIN_EMAILS,
	// ALLOWED_EMAILS and addresses on ALLOWED_EMAIL_DOMAINS may sign in.
	// ACCESS_ALLOWLIST unset means "enforce iff either list is non-empty", so
	// existing deployments that only set ALLOWED_EMAIL_DOMAINS keep working.
	AccessAllowlist     bool
	AllowedEmailDomains []string
	AllowedEmails       []string
	AdminEmails         []string
	DefaultRole         string

	DefaultProvider string
	YouTubeAPIKey   string

	// FallbackPlaylistURL seeds the default playlist (played when the queue is
	// empty) the first time the server starts with no playlist configured.
	// Managers can change it from the UI afterwards.
	FallbackPlaylistURL string
	FallbackShuffle     bool

	AuthDevMode bool

	// PlayerToken authenticates the headless player container. Empty disables the player socket.
	PlayerToken string

	// DatabaseURL is a Postgres URL. Empty means in-memory state (lost on restart).
	DatabaseURL string

	// Warnings collected while loading (e.g. generated secrets). Callers should log them.
	Warnings []string
}

// Load reads configuration from the process environment.
func Load() (*Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads configuration using the supplied lookup function.
func LoadFrom(getenv func(string) string) (*Config, error) {
	c := &Config{
		Env:                 strings.ToLower(envOr(getenv, "APP_ENV", "development")),
		Addr:                envOr(getenv, "ADDR", ":8080"),
		BaseURL:             strings.TrimRight(envOr(getenv, "BASE_URL", "http://localhost:8080"), "/"),
		FrontendURL:         strings.TrimRight(envOr(getenv, "FRONTEND_URL", "http://localhost:3000"), "/"),
		SessionSecret:       getenv("SESSION_SECRET"),
		GoogleClientID:      getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:   getenv("GOOGLE_REDIRECT_URL"),
		AllowedEmailDomains: splitList(getenv("ALLOWED_EMAIL_DOMAINS")),
		AllowedEmails:       splitList(getenv("ALLOWED_EMAILS")),
		AdminEmails:         splitList(getenv("ADMIN_EMAILS")),
		DefaultRole:         strings.ToLower(envOr(getenv, "DEFAULT_ROLE", "listener")),
		DefaultProvider:     strings.ToLower(envOr(getenv, "DEFAULT_PROVIDER", "youtube")),
		YouTubeAPIKey:       getenv("YOUTUBE_API_KEY"),
		FallbackPlaylistURL: strings.TrimSpace(getenv("FALLBACK_PLAYLIST_URL")),
		PlayerToken:         strings.TrimSpace(getenv("PLAYER_TOKEN")),
		DatabaseURL:         strings.TrimSpace(getenv("DATABASE_URL")),
	}

	var err error
	if c.SessionTTL, err = parseDuration(getenv("SESSION_TTL"), 7*24*time.Hour); err != nil {
		return nil, fmt.Errorf("SESSION_TTL: %w", err)
	}
	if c.CookieSecure, err = parseBool(getenv("COOKIE_SECURE"), false); err != nil {
		return nil, fmt.Errorf("COOKIE_SECURE: %w", err)
	}
	if c.AuthDevMode, err = parseBool(getenv("AUTH_DEV_MODE"), false); err != nil {
		return nil, fmt.Errorf("AUTH_DEV_MODE: %w", err)
	}
	if c.FallbackShuffle, err = parseBool(getenv("FALLBACK_SHUFFLE"), true); err != nil {
		return nil, fmt.Errorf("FALLBACK_SHUFFLE: %w", err)
	}
	listsSet := len(c.AllowedEmailDomains) > 0 || len(c.AllowedEmails) > 0
	if c.AccessAllowlist, err = parseBool(getenv("ACCESS_ALLOWLIST"), listsSet); err != nil {
		return nil, fmt.Errorf("ACCESS_ALLOWLIST: %w", err)
	}

	if c.Env != "development" && c.Env != "production" {
		return nil, fmt.Errorf("APP_ENV must be development or production, got %q", c.Env)
	}
	if c.GoogleRedirectURL == "" {
		c.GoogleRedirectURL = c.BaseURL + "/api/v1/auth/google/callback"
	}
	for i, e := range c.AdminEmails {
		c.AdminEmails[i] = strings.ToLower(e)
	}
	for i, d := range c.AllowedEmailDomains {
		c.AllowedEmailDomains[i] = strings.ToLower(strings.TrimPrefix(d, "@"))
	}
	for i, e := range c.AllowedEmails {
		c.AllowedEmails[i] = strings.ToLower(e)
	}
	if c.AccessAllowlist && !listsSet && len(c.AdminEmails) == 0 {
		c.Warnings = append(c.Warnings, "ACCESS_ALLOWLIST is enabled but ALLOWED_EMAIL_DOMAINS, ALLOWED_EMAILS and ADMIN_EMAILS are all empty; nobody can sign in")
	}

	if c.IsProduction() {
		if c.AuthDevMode {
			return nil, errors.New("AUTH_DEV_MODE cannot be enabled when APP_ENV=production")
		}
		if c.SessionSecret == "" {
			return nil, errors.New("SESSION_SECRET is required when APP_ENV=production")
		}
	}
	if c.SessionSecret == "" {
		c.SessionSecret = randomSecret()
		c.Warnings = append(c.Warnings, "SESSION_SECRET not set; generated a random one (sessions will not survive restarts)")
	}
	if len(c.SessionSecret) < 32 {
		c.Warnings = append(c.Warnings, "SESSION_SECRET is shorter than 32 characters; use a longer secret")
	}
	if !c.GoogleConfigured() && !c.AuthDevMode {
		return nil, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required (or set AUTH_DEV_MODE=true for local development)")
	}
	if !c.GoogleConfigured() {
		c.Warnings = append(c.Warnings, "Google SSO not configured; only the dev login endpoint is available")
	}
	if c.AuthDevMode {
		c.Warnings = append(c.Warnings, "AUTH_DEV_MODE is enabled; POST /api/v1/auth/dev/login issues sessions without Google")
	}
	if c.DatabaseURL == "" {
		if c.IsProduction() {
			return nil, errors.New("DATABASE_URL is required when APP_ENV=production")
		}
		c.Warnings = append(c.Warnings, "DATABASE_URL not set; users and queue are kept in memory and lost on restart")
	}
	if c.PlayerToken == "" {
		c.Warnings = append(c.Warnings, "PLAYER_TOKEN not set; no player can connect, nothing will actually play")
	} else if len(c.PlayerToken) < 16 {
		c.Warnings = append(c.Warnings, "PLAYER_TOKEN is shorter than 16 characters; use a longer secret")
	}
	return c, nil
}

// IsProduction reports whether the app runs with APP_ENV=production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

// GoogleConfigured reports whether Google SSO credentials are present.
func (c *Config) GoogleConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func envOr(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

func parseBool(s string, def bool) (bool, error) {
	if strings.TrimSpace(s) == "" {
		return def, nil
	}
	return strconv.ParseBool(s)
}

func randomSecret() string {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		panic("config: crypto/rand failed: " + err.Error())
	}
	return base64.RawStdEncoding.EncodeToString(b)
}
