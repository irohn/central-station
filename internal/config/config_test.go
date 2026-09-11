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
