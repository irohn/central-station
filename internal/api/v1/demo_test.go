package v1

import (
	"net/http/httptest"
	"testing"
	"time"

	"central-station/internal/alerts"
	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/config"
	"central-station/internal/provider"
	"central-station/internal/settings"
	"central-station/internal/store"
	"central-station/internal/tts"
)

// newPasswordAPIWithUsers is newPasswordAPI on a caller-supplied user store,
// so a second instance can be started "after a restart" with the same users.
func newPasswordAPIWithUsers(t *testing.T, extra map[string]string, users *store.Memory) *testAPI {
	t.Helper()
	env := map[string]string{"AUTH_PASSWORD_ENABLED": "true", "PLAYER_TOKEN": "test-player-token-123", "ADMIN_EMAILS": "boss@corp.io"}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := provider.NewRegistry("youtube", fakeProvider{})
	srv := httptest.NewServer(Handler(Deps{Config: cfg, Users: users, Zones: testZones(t, cfg, reg), Providers: reg,
		Clips: clips.NewMemory(), TTS: tts.New(""), Settings: settings.NewMemory(), Alerts: alerts.NewMemory(),
		Sessions: auth.NewSessions(cfg.SessionSecret, time.Hour, false)}))
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv}
}

// newDemoAPI runs the API in demo mode: self-service registration, no
// Google, no verification; boss@corp.io is the pre-set admin. Google
// credentials are deliberately present to prove they are ignored.
func newDemoAPI(t *testing.T, extra map[string]string) *testAPI {
	t.Helper()
	return newDemoAPIWithUsers(t, extra, store.NewMemory())
}

func newDemoAPIWithUsers(t *testing.T, extra map[string]string, users *store.Memory) *testAPI {
	t.Helper()
	env := map[string]string{"AUTH_DEMO_MODE": "true", "GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "secret"}
	for k, v := range extra {
		env[k] = v
	}
	return newPasswordAPIWithUsers(t, env, users)
}

func TestDemoModeRegisterIsCollaboratorAtOnce(t *testing.T) {
	a := newDemoAPI(t, nil)
	r := a.do("GET", "/auth/methods", "", nil)
	if r.Body["demo"] != true || r.Body["password"] != true || r.Body["google"] != false || r.Body["dev"] != false || r.Body["passwordVerification"] != "none" {
		t.Fatalf("methods: %v", r.Body)
	}

	r = a.do("POST", "/auth/password/register", "", map[string]string{"email": "Pat@Example.com", "name": "Pat", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["verification"] != "none" || r.Body["verificationSent"] != false {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	user := r.Body["user"].(map[string]any)
	tok := r.Body["token"].(string)
	if user["emailVerified"] != true || user["role"] != "collaborator" || user["hasPassword"] != true {
		t.Fatalf("demo user must be a verified collaborator: %v", user)
	}
	// Collaborator permissions apply immediately: the new user can add songs.
	if r := a.do("POST", "/queue", tok, map[string]string{"input": "https://www.youtube.com/watch?v=abc123"}); r.Status != 201 {
		t.Fatalf("queue add as demo user: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/password/login", "", map[string]string{"email": "pat@example.com", "password": "a decent passphrase"}); r.Status != 200 || r.Body["verification"] != "none" {
		t.Fatalf("login: %d %v", r.Status, r.Body)
	}

	// The verification machinery is not mounted.
	if r := a.do("POST", "/auth/verify/resend", tok, nil); r.Status != 404 && r.Status != 405 {
		t.Fatalf("resend should not exist: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/auth/verify?token=x", "", nil); r.Status != 404 && r.Status != 405 {
		t.Fatalf("verify link should not exist: %d %v", r.Status, r.Body)
	}
	// Google is off even though credentials are configured.
	if r := a.do("GET", "/auth/google/login", "", nil); r.Status != 501 || errCode(r) != "sso_disabled" {
		t.Fatalf("google login: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/auth/google/callback?code=x&state=y", "", nil); r.Status != 501 || errCode(r) != "sso_disabled" {
		t.Fatalf("google callback: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/auth/dev/login", "", map[string]string{"email": "x@example.com", "role": "admin"}); r.Status != 404 && r.Status != 405 {
		t.Fatalf("dev login should be off: %d", r.Status)
	}
}

func TestDemoModeAdminEmailBecomesAdmin(t *testing.T) {
	a := newDemoAPI(t, nil)
	r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "boss@corp.io", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["user"].(map[string]any)["role"] != "admin" {
		t.Fatalf("admin email must be admin on registration: %d %v", r.Status, r.Body)
	}
	admin := r.Body["token"].(string)

	r = a.do("POST", "/auth/password/register", "", map[string]string{"email": "pat@example.com", "password": "a decent passphrase"})
	if r.Status != 200 {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
	pat := r.Body["user"].(map[string]any)
	patTok := r.Body["token"].(string)

	// The admin changes the collaborator's role later; the user cannot.
	if r := a.do("PATCH", "/users/"+pat["id"].(string), patTok, map[string]any{"role": "manager"}); r.Status != 403 {
		t.Fatalf("self-promotion: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+pat["id"].(string), admin, map[string]any{"role": "manager"}); r.Status != 200 || r.Body["role"] != "manager" {
		t.Fatalf("admin sets role: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+pat["id"].(string), admin, map[string]any{"role": "listener"}); r.Status != 200 || r.Body["role"] != "listener" {
		t.Fatalf("admin demotes: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/me", patTok, nil); r.Status != 200 || r.Body["role"] != "listener" {
		t.Fatalf("role change is live: %d %v", r.Status, r.Body)
	}
}

func TestDemoModeVerifiedRoleConfigurable(t *testing.T) {
	a := newDemoAPI(t, map[string]string{"AUTH_VERIFIED_ROLE": "manager"})
	r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "pat@example.com", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["user"].(map[string]any)["role"] != "manager" {
		t.Fatalf("register: %d %v", r.Status, r.Body)
	}
}

func TestDemoModeDomainEnforcement(t *testing.T) {
	// Domain list set: only that domain (and the admin) may register.
	a := newDemoAPI(t, map[string]string{"ALLOWED_EMAIL_DOMAINS": "domain.com"})
	for email, want := range map[string]int{"ann@domain.com": 200, "Bob@DOMAIN.com": 200, "boss@corp.io": 200, "eve@gmail.com": 403, "eve@sub.domain.com": 403} {
		r := a.do("POST", "/auth/password/register", "", map[string]string{"email": email, "password": "a decent passphrase"})
		if r.Status != want {
			t.Errorf("register %s -> %d, want %d (%v)", email, r.Status, want, r.Body)
		}
	}

	// Enforcement switched off: the same list is on file but anyone may register.
	a = newDemoAPI(t, map[string]string{"ALLOWED_EMAIL_DOMAINS": "domain.com", "ACCESS_ALLOWLIST": "false"})
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "eve@gmail.com", "password": "a decent passphrase"}); r.Status != 200 {
		t.Errorf("allow-list off: %d %v", r.Status, r.Body)
	}

	// No list at all: open registration.
	a = newDemoAPI(t, nil)
	if r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "eve@gmail.com", "password": "a decent passphrase"}); r.Status != 200 {
		t.Errorf("open: %d %v", r.Status, r.Body)
	}
}

func TestDemoModeLoginVerifiesOlderAccounts(t *testing.T) {
	// An account registered before demo mode (unverified listener) is
	// promoted the next time it signs in.
	users := store.NewMemory()
	a := newPasswordAPIWithUsers(t, nil, users)
	r := a.do("POST", "/auth/password/register", "", map[string]string{"email": "old@example.com", "password": "a decent passphrase"})
	if r.Status != 200 || r.Body["user"].(map[string]any)["emailVerified"] != false {
		t.Fatalf("pre-demo register: %d %v", r.Status, r.Body)
	}
	// Restart in demo mode on the same user store.
	b := newDemoAPIWithUsers(t, nil, users)
	r = b.do("POST", "/auth/password/login", "", map[string]string{"email": "old@example.com", "password": "a decent passphrase"})
	u := r.Body["user"].(map[string]any)
	if r.Status != 200 || u["emailVerified"] != true || u["role"] != "collaborator" {
		t.Fatalf("login after switching to demo: %d %v", r.Status, r.Body)
	}
}
