package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDevDefaults(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.FrontendURL != "http://localhost:3000" || c.DefaultProvider != "youtube" || c.SessionSecret == "" {
		t.Fatalf("config = %+v", c)
	}
	if c.GoogleRedirectURL != "http://localhost:8080/api/v1/auth/google/callback" {
		t.Fatalf("redirect = %s", c.GoogleRedirectURL)
	}
}

func TestRequiresGoogleOrDevMode(t *testing.T) {
	_, err := LoadFrom(env(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "GOOGLE_CLIENT_ID") {
		t.Fatalf("err = %v", err)
	}
	// Password sign-in alone is a valid method.
	c, err := LoadFrom(env(map[string]string{"AUTH_PASSWORD_ENABLED": "true"}))
	if err != nil || !c.AuthPasswordEnabled || c.AuthUnverifiedRole != "listener" || c.AuthVerifiedRole != "collaborator" || c.SMTPPort != 587 {
		t.Fatalf("password only: %v %+v", err, c)
	}
	if _, err := LoadFrom(env(map[string]string{"AUTH_PASSWORD_ENABLED": "true", "AUTH_VERIFIED_ROLE": "owner"})); err == nil {
		t.Fatal("bad role accepted")
	}
	if _, err := LoadFrom(env(map[string]string{"AUTH_PASSWORD_ENABLED": "true", "SMTP_HOST": "smtp.example.com"})); err == nil {
		t.Fatal("SMTP_HOST without SMTP_FROM accepted")
	}
}

func TestProductionGuards(t *testing.T) {
	base := map[string]string{"APP_ENV": "production", "GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "s"}
	if _, err := LoadFrom(env(base)); err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Fatalf("err = %v", err)
	}
	base["SESSION_SECRET"] = strings.Repeat("x", 40)
	if _, err := LoadFrom(env(base)); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
	base["DATABASE_URL"] = "postgres://x@db/x"
	base["SESSION_SECRET"] = strings.Repeat("x", 40)
	base["AUTH_DEV_MODE"] = "true"
	if _, err := LoadFrom(env(base)); err == nil || !strings.Contains(err.Error(), "AUTH_DEV_MODE") {
		t.Fatalf("err = %v", err)
	}
	delete(base, "AUTH_DEV_MODE")
	base["ADMIN_EMAILS"] = " Boss@Example.com , ops@example.com"
	base["ALLOWED_EMAIL_DOMAINS"] = "@example.com"
	c, err := LoadFrom(env(base))
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminEmails[0] != "boss@example.com" || c.AllowedEmailDomains[0] != "example.com" {
		t.Fatalf("lists = %v %v", c.AdminEmails, c.AllowedEmailDomains)
	}
	if !c.AccessAllowlist {
		t.Fatal("allow-list should be enforced when a list is set and ACCESS_ALLOWLIST is unset")
	}
}

func TestRolePermissions(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "ROLE_PERMISSIONS_COLLABORATOR": " queue:add, Volume:Control ", "ROLE_PERMISSIONS_MANAGER": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.RolePermissions) != 1 || len(c.RolePermissions["collaborator"]) != 2 || c.RolePermissions["collaborator"][1] != "volume:control" {
		t.Fatalf("role permissions = %v", c.RolePermissions)
	}
	if _, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "ROLE_PERMISSIONS_LISTENER": "queue:read,nope:x"})); err == nil {
		t.Fatal("unknown permission accepted")
	}
}

func TestAllowedOrigins(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "ALLOWED_ORIGINS": " http://mac.tailnet.ts.net:3000/, https://office.local "}))
	if err != nil || len(c.AllowedOrigins) != 2 || c.AllowedOrigins[0] != "http://mac.tailnet.ts.net:3000" || c.AllowedOrigins[1] != "https://office.local" {
		t.Fatalf("origins: %v %v", err, c.AllowedOrigins)
	}
	if _, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "ALLOWED_ORIGINS": "mac.tailnet.ts.net:3000"})); err == nil {
		t.Fatal("origin without scheme accepted")
	}
}

func TestAlertsTimezone(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true"}))
	if err != nil || c.AlertsTimezone != "Asia/Jerusalem" {
		t.Fatalf("default: %v %q", err, c.AlertsTimezone)
	}
	if _, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "ALERTS_TIMEZONE": "Mars/Olympus"})); err == nil {
		t.Fatal("bad timezone accepted")
	}
}

func TestClipLimits(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true"}))
	if err != nil || c.ClipsMaxSaved != 50 || c.ClipsMaxBytes != 5<<20 || c.ClipsMaxDurationSeconds != 60 || c.TTSMaxChars != 600 {
		t.Fatalf("defaults: %v %+v", err, c)
	}
	c, err = LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "CLIPS_MAX_SAVED": "5", "TTS_MAX_CHARS": "100"}))
	if err != nil || c.ClipsMaxSaved != 5 || c.TTSMaxChars != 100 {
		t.Fatalf("overrides: %v %+v", err, c)
	}
	if _, err := LoadFrom(env(map[string]string{"AUTH_DEV_MODE": "true", "CLIPS_MAX_SAVED": "0"})); err == nil {
		t.Fatal("zero accepted")
	}
}

func TestAccessAllowlistFlag(t *testing.T) {
	base := map[string]string{"AUTH_DEV_MODE": "true"}
	if c, _ := LoadFrom(env(base)); c.AccessAllowlist {
		t.Fatal("nothing set: should be open")
	}
	base["ALLOWED_EMAILS"] = "A@b.com"
	base["ACCESS_ALLOWLIST"] = "false"
	c, err := LoadFrom(env(base))
	if err != nil || c.AccessAllowlist || c.AllowedEmails[0] != "a@b.com" {
		t.Fatalf("explicit false: %v %+v", err, c)
	}
	base["ACCESS_ALLOWLIST"] = "maybe"
	if _, err := LoadFrom(env(base)); err == nil {
		t.Fatal("bad bool accepted")
	}
}

func TestProxyMode(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{"AUTH_MODE": "proxy", "AUTH_TRUSTED_PROXY_CIDRS": "10.42.0.2/31, 10.42.0.4/30,192.168.1.9", "ALLOWED_EMAIL_DOMAINS": "example.com"}
	}
	c, err := LoadFrom(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if !c.ProxyMode() || c.TrustedHeader != "Tailscale-User-Login" || c.DisplayNameHeader != "Tailscale-User-Name" || c.PictureHeader != "Tailscale-User-Profile-Pic" {
		t.Fatalf("config = %+v", c)
	}
	if len(c.TrustedProxyCIDRs) != 3 || c.TrustedProxyCIDRs[0].String() != "10.42.0.2/31" || c.TrustedProxyCIDRs[2].String() != "192.168.1.9/32" {
		t.Fatalf("cidrs = %v", c.TrustedProxyCIDRs)
	}
	if !c.AccessAllowlist {
		t.Fatal("allow-list must be enforced")
	}

	// Production needs neither SESSION_SECRET nor Google in this mode.
	e := base()
	e["APP_ENV"] = "production"
	e["DATABASE_URL"] = "postgres://x@db/x"
	e["PLAYER_TOKEN"] = strings.Repeat("p", 24)
	if c, err := LoadFrom(env(e)); err != nil || len(c.Warnings) != 0 {
		t.Fatalf("production: %v %v", err, c.Warnings)
	}

	// Optional headers can be switched off; the identity header cannot.
	e = base()
	e["AUTH_DISPLAY_NAME_HEADER"] = "none"
	e["AUTH_PICTURE_HEADER"] = "NONE"
	e["AUTH_TRUSTED_HEADER"] = "X-Login"
	if c, err := LoadFrom(env(e)); err != nil || c.DisplayNameHeader != "" || c.PictureHeader != "" || c.TrustedHeader != "X-Login" {
		t.Fatalf("headers: %v %+v", err, c)
	}

	for name, mut := range map[string]func(map[string]string){
		"no cidrs":       func(m map[string]string) { delete(m, "AUTH_TRUSTED_PROXY_CIDRS") },
		"bad cidr":       func(m map[string]string) { m["AUTH_TRUSTED_PROXY_CIDRS"] = "10.42.0.0/24,nope" },
		"dev mode":       func(m map[string]string) { m["AUTH_DEV_MODE"] = "true" },
		"password":       func(m map[string]string) { m["AUTH_PASSWORD_ENABLED"] = "true" },
		"no allow-list":  func(m map[string]string) { delete(m, "ALLOWED_EMAIL_DOMAINS") },
		"allow-list off": func(m map[string]string) { m["ACCESS_ALLOWLIST"] = "false" },
		"unknown mode":   func(m map[string]string) { m["AUTH_MODE"] = "magic" },
	} {
		e := base()
		mut(e)
		if _, err := LoadFrom(env(e)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Google credentials beside proxy mode are a warning, not a second way in.
	e = base()
	e["GOOGLE_CLIENT_ID"], e["GOOGLE_CLIENT_SECRET"] = "id", "s"
	c, err = LoadFrom(env(e))
	if err != nil || !strings.Contains(strings.Join(c.Warnings, "\n"), "GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are set but not used") {
		t.Fatalf("google beside proxy: %v %v", err, c.Warnings)
	}

	// Admin emails alone are an allow-list too.
	e = base()
	delete(e, "ALLOWED_EMAIL_DOMAINS")
	e["ADMIN_EMAILS"] = "boss@example.com"
	if _, err := LoadFrom(env(e)); err != nil {
		t.Fatalf("admins only: %v", err)
	}
}
