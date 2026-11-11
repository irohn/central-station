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
	"central-station/internal/provider"
	"central-station/internal/settings"
	"central-station/internal/store"
	"central-station/internal/tts"
)

// newProxyAPI builds the API in proxy mode. httptest connects from the
// loopback, so cidrs decides whether the test client counts as the proxy.
func newProxyAPI(t *testing.T, cidrs string, extra map[string]string) *testAPI {
	t.Helper()
	env := map[string]string{
		"AUTH_MODE": "proxy", "AUTH_TRUSTED_PROXY_CIDRS": cidrs,
		"ALLOWED_EMAIL_DOMAINS": "example.com", "ADMIN_EMAILS": "root@example.com",
		"PLAYER_TOKEN": "test-player-token-123", "FRONTEND_URL": "https://central.example.ts.net", "BASE_URL": "https://central.example.ts.net",
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := provider.NewRegistry("youtube", fakeProvider{})
	cs := clips.NewMemory()
	if err := clips.EnsureBuiltins(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(Deps{
		Config: cfg, Users: store.NewMemory(), Zones: testZones(t, cfg, reg), Clips: cs, TTS: tts.New(""),
		Settings: settings.NewMemory(), Alerts: alerts.NewMemory(), Providers: reg,
		Sessions: auth.NewSessions(cfg.SessionSecret, time.Hour, false),
	}))
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv}
}

const loopback = "127.0.0.1/32,::1/128"

func TestProxyModeSignsInFromHeader(t *testing.T) {
	a := newProxyAPI(t, loopback, nil)
	as := func(email, name string) []string {
		return []string{"Tailscale-User-Login", email, "Tailscale-User-Name", name, "Tailscale-User-Profile-Pic", "https://pics.example.com/" + name + ".png"}
	}

	// No header: anonymous, and the reason is on record.
	if r := a.do("GET", "/me", "", nil); r.Status != 401 || errCode(r) != "no_identity" {
		t.Fatalf("no header: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/auth/whoami", "", nil); r.Status != 200 || r.Body["signedIn"] != false || r.Body["reason"] != "no_identity" || r.Body["mode"] != "proxy" {
		t.Fatalf("whoami anonymous: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/auth/methods", "", nil); r.Status != 200 || r.Body["proxy"] != true || r.Body["google"] != false || r.Body["dev"] != false {
		t.Fatalf("methods: %d %v", r.Status, r.Body)
	}

	// First request creates the user with DEFAULT_ROLE and the proxy's profile.
	r := a.do("GET", "/me", "", nil, as("Ann@Example.com", "Ann A")...)
	if r.Status != 200 || r.Body["email"] != "ann@example.com" || r.Body["name"] != "Ann A" || r.Body["role"] != "listener" || r.Body["emailVerified"] != true || r.Body["pictureUrl"] != "https://pics.example.com/Ann A.png" {
		t.Fatalf("first request: %d %v", r.Status, r.Body)
	}
	annID := r.Body["id"].(string)
	// The second one finds the same user; a renamed profile is picked up.
	if r := a.do("GET", "/me", "", nil, as("ann@example.com", "Ann B")...); r.Status != 200 || r.Body["id"] != annID || r.Body["name"] != "Ann B" {
		t.Fatalf("second request: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/auth/whoami", "", nil, as("ann@example.com", "Ann B")...); r.Status != 200 || r.Body["signedIn"] != true || r.Body["user"].(map[string]any)["id"] != annID {
		t.Fatalf("whoami signed in: %d %v", r.Status, r.Body)
	}
	// A name chosen in the app survives the proxy's profile name.
	if r := a.do("PATCH", "/users/"+annID, "", map[string]string{"name": "Annie"}, as("ann@example.com", "Ann B")...); r.Status != 200 || r.Body["name"] != "Annie" {
		t.Fatalf("rename: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/me", "", nil, as("ann@example.com", "Ann C")...); r.Status != 200 || r.Body["name"] != "Annie" || r.Body["displayName"] != "Annie" {
		t.Fatalf("display name lost: %d %v", r.Status, r.Body)
	}

	// ADMIN_EMAILS are admins on their first request, and stay admins if demoted in the store.
	r = a.do("GET", "/me", "", nil, as("root@example.com", "Root")...)
	if r.Status != 200 || r.Body["role"] != "admin" {
		t.Fatalf("admin: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/users", "", nil, as("root@example.com", "Root")...); r.Status != 200 || len(r.Body["users"].([]any)) != 2 {
		t.Fatalf("users: %d %v", r.Status, r.Body)
	}

	// Allow-list and identity checks.
	if r := a.do("GET", "/me", "", nil, "Tailscale-User-Login", "eve@evil.example"); r.Status != 401 || errCode(r) != "email_not_allowed" {
		t.Fatalf("wrong domain: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/queue", "", nil, "Tailscale-User-Login", "nobody"); r.Status != 401 || errCode(r) != "invalid_identity" {
		t.Fatalf("not an address: %d %v", r.Status, r.Body)
	}

	// Sessions, cookies and Bearer tokens are not a way in.
	sessions := auth.NewSessions("x", time.Hour, false)
	tok, _, _ := sessions.Issue(annID)
	if r := a.do("GET", "/me", tok, nil); r.Status != 401 || errCode(r) != "no_identity" {
		t.Fatalf("bearer: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/me", "", nil, "Cookie", auth.SessionCookie+"="+tok); r.Status != 401 || errCode(r) != "no_identity" {
		t.Fatalf("cookie: %d %v", r.Status, r.Body)
	}
	// ...and the login and logout endpoints are gone.
	for _, p := range []string{"/auth/google/login", "/auth/dev/login", "/auth/logout", "/auth/password/login"} {
		if r := a.do("POST", p, "", nil, as("ann@example.com", "Ann")...); r.Status != 404 {
			t.Errorf("%s: %d, want 404", p, r.Status)
		}
	}
}

func TestProxyModeRefusesUntrustedPeer(t *testing.T) {
	// The proxy pods' range; the test client (loopback) is the node.
	a := newProxyAPI(t, "10.42.0.2/31,10.42.0.4/30", nil)
	r := a.do("GET", "/me", "", nil, "Tailscale-User-Login", "root@example.com")
	if r.Status != 401 || errCode(r) != "untrusted_peer" {
		t.Fatalf("node with admin header: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/auth/whoami", "", nil, "Tailscale-User-Login", "root@example.com"); r.Status != 200 || r.Body["signedIn"] != false || r.Body["reason"] != "untrusted_peer" {
		t.Fatalf("whoami: %d %v", r.Status, r.Body)
	}
}

func TestProxyModeWritesNeedSameSite(t *testing.T) {
	a := newProxyAPI(t, loopback, nil)
	hdr := func(extra ...string) []string {
		return append([]string{"Tailscale-User-Login", "col@example.com", "Tailscale-User-Name", "Col"}, extra...)
	}
	// Promote to collaborator first (as the admin, same-origin).
	r := a.do("GET", "/me", "", nil, hdr()...)
	colID := r.Body["id"].(string)
	if r := a.do("PATCH", "/users/"+colID, "", map[string]string{"role": "collaborator"}, "Tailscale-User-Login", "root@example.com", "Origin", "https://central.example.ts.net"); r.Status != 200 {
		t.Fatalf("promote: %d %v", r.Status, r.Body)
	}
	body := map[string]string{"input": "https://www.youtube.com/watch?v=abc"}
	if r := a.do("POST", "/queue", "", body, hdr("Origin", "https://evil.example")...); r.Status != 403 || !strings.Contains(r.Body["error"].(map[string]any)["message"].(string), "cross-site") {
		t.Fatalf("cross-site origin: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/queue", "", body, hdr("Sec-Fetch-Site", "cross-site")...); r.Status != 403 {
		t.Fatalf("cross-site fetch: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/queue", "", body, hdr("Origin", "https://central.example.ts.net")...); r.Status != 201 {
		t.Fatalf("same-site: %d %v", r.Status, r.Body)
	}
	// A client that sends no Origin (curl from the person's own machine) is not a browser.
	if r := a.do("POST", "/queue", "", map[string]string{"input": "https://www.youtube.com/watch?v=def"}, hdr()...); r.Status != 201 {
		t.Fatalf("no origin: %d %v", r.Status, r.Body)
	}
	// Reads never need it.
	if r := a.do("GET", "/queue", "", nil, hdr("Origin", "https://evil.example")...); r.Status != 200 {
		t.Fatalf("read: %d %v", r.Status, r.Body)
	}
}

func TestProxyModeLeavesPlayerTokenAlone(t *testing.T) {
	a := newProxyAPI(t, loopback, nil)
	// The player socket is outside the session middleware: an identity header
	// is not a player token, and the token works without one.
	req, _ := http.NewRequest("GET", a.srv.URL+"/player/ws", nil)
	req.Header.Set("Tailscale-User-Login", "root@example.com")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("player ws with identity header: %d", res.StatusCode)
	}
	r := a.do("GET", "/player/clips/nope/audio", "test-player-token-123", nil)
	if r.Status == 401 {
		t.Fatalf("player token refused: %d %v", r.Status, r.Body)
	}
}
