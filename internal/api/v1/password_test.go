package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"central-station/internal/alerts"
	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/config"
	"central-station/internal/fallback"
	"central-station/internal/provider"
	"central-station/internal/queue"
	"central-station/internal/settings"
	"central-station/internal/store"
	"central-station/internal/tts"
)

func newPasswordAPI(t *testing.T, extra map[string]string) *testAPI {
	t.Helper()
	env := map[string]string{"AUTH_PASSWORD_ENABLED": "true", "AUTH_DEV_MODE": "true", "PLAYER_TOKEN": "test-player-token-123", "ADMIN_EMAILS": "boss@corp.io"}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := provider.NewRegistry("youtube", fakeProvider{})
	q := queue.NewMemory()
	fb, _ := fallback.New(context.Background(), settings.NewMemory(), reg, nil)
	srv := httptest.NewServer(Handler(Deps{Config: cfg, Users: store.NewMemory(), Queue: q, Hub: newTestHub(q), Fallback: fb, Providers: reg,
		Clips: clips.NewMemory(), TTS: tts.New(""), Settings: settings.NewMemory(), Alerts: alerts.NewMemory(),
		Sessions: auth.NewSessions(cfg.SessionSecret, time.Hour, false)}))
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv}
}

func TestPasswordRegisterLoginVerify(t *testing.T) {
	a := newPasswordAPI(t, nil)
	if r := a.do("GET", "/auth/methods", "", nil); r.Body["password"] != true || r.Body["passwordVerification"] != "admin" {
		t.Fatalf("methods: %v", r.Body)
	}
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "not-an-email", "password": "long enough pw"}); r.Status != 400 || errCode(r) != "invalid_email" {
		t.Fatalf("bad email: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "pat@example.com", "password": "short"}); r.Status != 400 || errCode(r) != "weak_password" {
		t.Fatalf("weak: %d %v", r.Status, r.Body)
	}
	r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "Pat@Example.com", "name": "Pat", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["verification"] != "admin" || r.Body["verificationSent"] != false {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	user := r.Body["user"].(map[string]any)
	tok := r.Body["token"].(string)
	if user["email"] != "pat@example.com" || user["emailVerified"] != false || user["role"] != "listener" || user["hasPassword"] != true {
		t.Fatalf("new user: %v", user)
	}
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "pat@example.com", "password": "another passphrase"}); r.Status != 409 || errCode(r) != "email_taken" {
		t.Fatalf("duplicate: %d %v", r.Status, r.Body)
	}

	// Login: wrong password, unknown account and provider-only accounts all answer the same.
	if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "pat@example.com", "password": "wrong password!"}); r.Status != 401 {
		t.Fatalf("wrong pw: %d", r.Status)
	}
	if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "nobody@example.com", "password": "wrong password!"}); r.Status != 401 {
		t.Fatalf("unknown: %d", r.Status)
	}
	a.login("g@example.com", "listener")
	if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "g@example.com", "password": "wrong password!"}); r.Status != 401 {
		t.Fatalf("provider-only: %d", r.Status)
	}
	r = a.do("POST", "/auth/password/login", "", map[string]string{"email": "PAT@example.com", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["user"].(map[string]any)["id"] != user["id"] {
		t.Fatalf("login: %d %v", r.Status, r.Body)
	}

	if r := a.do("GET", "/me", tok, nil); r.Status != 200 || r.Body["emailVerified"] != false {
		t.Fatalf("me: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/verify/resend", tok, nil); r.Status != 409 || errCode(r) != "verification_by_admin" {
		t.Fatalf("resend: %d %v", r.Status, r.Body)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noRedirect.Get(a.srv.URL + "/auth/verify?token=nope")
	if err != nil || res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "verification_link_invalid") {
		t.Fatalf("bad link: %v %v", err, res)
	}
	res.Body.Close()

	// Admin verifies -> promoted to the verified role (collaborator).
	admin, _ := a.login("root@example.com", "admin")
	if r := a.do("PATCH", "/users/"+user["id"].(string), admin, map[string]any{"emailVerified": true}); r.Status != 200 || r.Body["emailVerified"] != true || r.Body["role"] != "collaborator" {
		t.Fatalf("admin verify: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+user["id"].(string), admin, map[string]any{"role": "manager"}); r.Status != 200 || r.Body["role"] != "manager" {
		t.Fatalf("set role: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+user["id"].(string), admin, map[string]any{"emailVerified": false}); r.Status != 200 || r.Body["emailVerified"] != false || r.Body["role"] != "manager" {
		t.Fatalf("unverify: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+user["id"].(string), admin, map[string]any{"emailVerified": true}); r.Body["role"] != "manager" {
		t.Fatalf("re-verify must not demote: %v", r.Body)
	}
	if r := a.do("PATCH", "/users/"+user["id"].(string), admin, map[string]any{}); r.Status != 400 {
		t.Fatalf("empty patch: %d", r.Status)
	}

	if r := a.do("POST", "/auth/password/change", tok, map[string]string{"currentPassword": "nope nope nope", "newPassword": "brand new passphrase"}); r.Status != 403 {
		t.Fatalf("change wrong current: %d", r.Status)
	}
	if r := a.do("POST", "/auth/password/change", tok, map[string]string{"currentPassword": "a decent passphrase", "newPassword": "brand new passphrase"}); r.Status != 204 {
		t.Fatalf("change: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "pat@example.com", "password": "brand new passphrase"}); r.Status != 200 {
		t.Fatalf("login with new: %d", r.Status)
	}
}

func TestAdminEmailNeedsVerification(t *testing.T) {
	a := newPasswordAPI(t, nil)
	r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "boss@corp.io", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["user"].(map[string]any)["role"] != "listener" {
		t.Fatalf("unverified admin email must not be admin: %v", r.Body)
	}
	id := r.Body["user"].(map[string]any)["id"].(string)
	admin, _ := a.login("root@example.com", "admin")
	if r := a.do("PATCH", "/users/"+id, admin, map[string]any{"emailVerified": true}); r.Body["role"] != "admin" {
		t.Fatalf("verified admin email should be admin: %v", r.Body)
	}
}

func TestPasswordLoginRateLimit(t *testing.T) {
	a := newPasswordAPI(t, nil)
	a.do("POST", "/auth/password/register", "", map[string]string{"email": "pat@example.com", "password": "a decent passphrase"})
	for i := 0; i < 10; i++ {
		if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "pat@example.com", "password": "wrong"}); r.Status != 401 {
			t.Fatalf("attempt %d: %d", i, r.Status)
		}
	}
	if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "pat@example.com", "password": "a decent passphrase"}); r.Status != 429 {
		t.Fatalf("expected lockout, got %d", r.Status)
	}
}

func TestPasswordRegisterAllowlist(t *testing.T) {
	a := newPasswordAPI(t, map[string]string{"ALLOWED_EMAIL_DOMAINS": "corp.io", "ACCESS_ALLOWLIST": "true"})
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "x@gmail.com", "password": "a decent passphrase"}); r.Status != 403 {
		t.Fatalf("outside allowlist: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "x@corp.io", "password": "a decent passphrase"}); r.Status != 200 {
		t.Fatalf("inside allowlist: %d %v", r.Status, r.Body)
	}
}

func TestPasswordDisabledByDefault(t *testing.T) {
	a := newTestAPI(t)
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "x@corp.io", "password": "a decent passphrase"}); r.Status != 404 {
		t.Fatalf("register with flag off: %d", r.Status)
	}
	if r := a.do("GET", "/auth/methods", "", nil); r.Body["password"] != false {
		t.Fatalf("methods: %v", r.Body)
	}
}
