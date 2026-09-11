package v1

import (
	"context"
	"testing"
	"time"

	"central-station/internal/auth"
	"central-station/internal/config"
	"central-station/internal/fallback"
	"central-station/internal/provider"
	"central-station/internal/queue"
	"central-station/internal/settings"
	"central-station/internal/store"
	"net/http/httptest"
)

func newAllowlistAPI(t *testing.T, env map[string]string) *testAPI {
	t.Helper()
	env["AUTH_DEV_MODE"] = "true"
	env["PLAYER_TOKEN"] = "test-player-token-123"
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := provider.NewRegistry("youtube", fakeProvider{})
	q := queue.NewMemory()
	fb, _ := fallback.New(context.Background(), settings.NewMemory(), reg, nil)
	srv := httptest.NewServer(Handler(Deps{Config: cfg, Users: store.NewMemory(), Queue: q, Hub: newTestHub(q), Fallback: fb, Providers: reg,
		Sessions: auth.NewSessions(cfg.SessionSecret, time.Hour, false)}))
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv}
}

func TestAllowlist(t *testing.T) {
	try := func(a *testAPI, email string) int {
		return a.do("POST", "/auth/dev/login", "", map[string]string{"email": email, "role": "listener"}).Status
	}

	// Lists set, flag unset: enforced (backwards compatible).
	a := newAllowlistAPI(t, map[string]string{"ALLOWED_EMAIL_DOMAINS": "example.com", "ALLOWED_EMAILS": "Guest@Gmail.com", "ADMIN_EMAILS": "boss@corp.io"})
	for email, want := range map[string]int{"ann@example.com": 200, "guest@gmail.com": 200, "boss@corp.io": 200, "stranger@gmail.com": 403, "ann@sub.example.com": 403} {
		if got := try(a, email); got != want {
			t.Errorf("enforced: %s -> %d, want %d", email, got, want)
		}
	}

	// Same lists, flag explicitly off: open to everyone.
	a = newAllowlistAPI(t, map[string]string{"ALLOWED_EMAIL_DOMAINS": "example.com", "ACCESS_ALLOWLIST": "false"})
	if got := try(a, "stranger@gmail.com"); got != 200 {
		t.Errorf("disabled: %d", got)
	}

	// Flag on with only admins: admins still get in, nobody else does.
	a = newAllowlistAPI(t, map[string]string{"ACCESS_ALLOWLIST": "true", "ADMIN_EMAILS": "boss@corp.io"})
	if got := try(a, "boss@corp.io"); got != 200 {
		t.Errorf("admin: %d", got)
	}
	if got := try(a, "someone@corp.io"); got != 403 {
		t.Errorf("non-admin: %d", got)
	}
}
