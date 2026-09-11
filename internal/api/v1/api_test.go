package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"central-station/internal/auth"
	"central-station/internal/config"
	"central-station/internal/fallback"
	"central-station/internal/player"
	"central-station/internal/provider"
	"central-station/internal/queue"
	"central-station/internal/settings"
	"central-station/internal/store"
)

// fakeProvider stands in for YouTube so tests never hit the network.
type fakeProvider struct{}

func (fakeProvider) ID() string               { return "youtube" }
func (fakeProvider) Name() string             { return "YouTube" }
func (fakeProvider) MatchURL(u *url.URL) bool { return u.Hostname() == "www.youtube.com" }
func (fakeProvider) ResolveURL(_ context.Context, u *url.URL) (*provider.Track, error) {
	id := u.Query().Get("v")
	if id == "" {
		return nil, fmt.Errorf("%w: not a video", provider.ErrInvalidURL)
	}
	if id == "missing" {
		return nil, fmt.Errorf("%w: gone", provider.ErrNotFound)
	}
	return &provider.Track{Provider: "youtube", ID: id, URL: u.String(), Title: "video " + id}, nil
}
func (fakeProvider) Search(_ context.Context, q string) (*provider.Track, error) {
	return &provider.Track{Provider: "youtube", ID: "search", Title: "result for " + q}, nil
}
func (fakeProvider) PlaylistIDFromURL(u *url.URL) (string, bool) {
	id := u.Query().Get("list")
	return id, id != ""
}
func (fakeProvider) ResolvePlaylist(_ context.Context, id string) (*provider.Playlist, error) {
	pl := &provider.Playlist{Provider: "youtube", ID: id, URL: "https://www.youtube.com/playlist?list=" + id, Title: "list " + id}
	for i := 0; i < 3; i++ {
		tid := fmt.Sprintf("%s-%d", id, i)
		pl.Tracks = append(pl.Tracks, &provider.Track{Provider: "youtube", ID: tid, URL: "https://www.youtube.com/watch?v=" + tid, Title: "track " + tid})
	}
	return pl, nil
}

type testAPI struct {
	t   *testing.T
	srv *httptest.Server
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	cfg, err := config.LoadFrom(func(k string) string {
		return map[string]string{"AUTH_DEV_MODE": "true", "ADMIN_EMAILS": "root@example.com", "PLAYER_TOKEN": "test-player-token-123"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := provider.NewRegistry("youtube", fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	q := queue.NewMemory()
	fb, err := fallback.New(context.Background(), settings.NewMemory(), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	fb.OnChange = q.Notify
	q.SetFallback(fb)
	h := Handler(Deps{
		Config:    cfg,
		Users:     store.NewMemory(),
		Queue:     q,
		Hub:       player.NewHub(q, nil),
		Fallback:  fb,
		Providers: reg,
		Sessions:  auth.NewSessions(cfg.SessionSecret, time.Hour, false),
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv}
}

type resp struct {
	Status int
	Body   map[string]any
	Header http.Header
}

func (a *testAPI) do(method, path, token string, body any, headers ...string) resp {
	a.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, a.srv.URL+path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	out := resp{Status: res.StatusCode, Header: res.Header}
	_ = json.NewDecoder(res.Body).Decode(&out.Body)
	return out
}

func (a *testAPI) login(email, role string) (token string, userID string) {
	a.t.Helper()
	r := a.do("POST", "/auth/dev/login", "", map[string]string{"email": email, "role": role})
	if r.Status != 200 {
		a.t.Fatalf("dev login %s: %d %v", email, r.Status, r.Body)
	}
	user := r.Body["user"].(map[string]any)
	return r.Body["token"].(string), user["id"].(string)
}

func errCode(r resp) string {
	if e, ok := r.Body["error"].(map[string]any); ok {
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

func TestAuthRequired(t *testing.T) {
	a := newTestAPI(t)
	if r := a.do("GET", "/queue", "", nil); r.Status != 401 {
		t.Fatalf("anonymous: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/me", "garbage", nil); r.Status != 401 {
		t.Fatalf("bad token: %d", r.Status)
	}
	if r := a.do("GET", "/nope", "", nil); r.Status != 404 || errCode(r) != "not_found" {
		t.Fatalf("unknown route: %d %v", r.Status, r.Body)
	}
	tok, _ := a.login("lis@example.com", "listener")
	r := a.do("GET", "/me", tok, nil)
	if r.Status != 200 || r.Body["role"] != "listener" || r.Header.Get("API-Version") != "1" {
		t.Fatalf("me: %d %v %v", r.Status, r.Body, r.Header)
	}
}

func TestQueuePermissions(t *testing.T) {
	a := newTestAPI(t)
	listener, _ := a.login("lis@example.com", "listener")
	collab, collabID := a.login("col@example.com", "collaborator")
	collab2, _ := a.login("col2@example.com", "collaborator")
	manager, _ := a.login("mgr@example.com", "manager")

	// Listener can read but not add.
	if r := a.do("POST", "/queue", listener, map[string]string{"input": "some song"}); r.Status != 403 || errCode(r) != "forbidden" {
		t.Fatalf("listener add: %d %v", r.Status, r.Body)
	}

	// Collaborator adds three items (first becomes current).
	add := func(tok, input string) string {
		r := a.do("POST", "/queue", tok, map[string]string{"input": input})
		if r.Status != 201 {
			t.Fatalf("add %q: %d %v", input, r.Status, r.Body)
		}
		return r.Body["item"].(map[string]any)["id"].(string)
	}
	cur := add(collab, "https://www.youtube.com/watch?v=6TWJaFD6R2s")
	own := add(collab, "daft punk")
	other := add(collab2, "https://www.youtube.com/watch?v=abc")

	r := a.do("GET", "/queue", listener, nil)
	if r.Status != 200 || r.Body["status"] != "playing" || len(r.Body["items"].([]any)) != 2 {
		t.Fatalf("queue: %d %v", r.Status, r.Body)
	}
	if r.Body["current"].(map[string]any)["id"] != cur || r.Body["current"].(map[string]any)["addedById"] != collabID {
		t.Fatalf("current wrong: %v", r.Body["current"])
	}

	// Collaborator cannot remove someone else's item, or skip.
	if r := a.do("DELETE", "/queue/"+other, collab, nil); r.Status != 403 {
		t.Fatalf("remove other's: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/skip", collab, nil); r.Status != 403 {
		t.Fatalf("collab skip: %d", r.Status)
	}
	if r := a.do("DELETE", "/queue/"+cur, collab, nil); r.Status != 409 || errCode(r) != "item_is_playing" {
		t.Fatalf("remove current: %d %v", r.Status, r.Body)
	}
	// ...but can remove their own.
	if r := a.do("DELETE", "/queue/"+own, collab, nil); r.Status != 204 {
		t.Fatalf("remove own: %d %v", r.Status, r.Body)
	}
	// Listener cannot remove anything.
	if r := a.do("DELETE", "/queue/"+other, listener, nil); r.Status != 403 {
		t.Fatalf("listener remove: %d", r.Status)
	}

	// Manager removes others', reorders, skips, pauses.
	x := add(collab, "x")
	if r := a.do("PATCH", "/queue/"+x, manager, map[string]int{"position": 0}); r.Status != 200 {
		t.Fatalf("reorder: %d %v", r.Status, r.Body)
	}
	items := a.do("GET", "/queue", listener, nil).Body["items"].([]any)
	if items[0].(map[string]any)["id"] != x {
		t.Fatalf("reorder not applied: %v", items)
	}
	if r := a.do("DELETE", "/queue/"+other, manager, nil); r.Status != 204 {
		t.Fatalf("manager remove: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/skip", manager, nil); r.Status != 200 || r.Body["current"].(map[string]any)["id"] != x {
		t.Fatalf("skip: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/pause", manager, nil); r.Status != 200 || r.Body["status"] != "paused" {
		t.Fatalf("pause: %d %v", r.Status, r.Body)
	}
	h := a.do("GET", "/history", listener, nil)
	if h.Status != 200 || len(h.Body["entries"].([]any)) != 1 {
		t.Fatalf("history: %d %v", h.Status, h.Body)
	}
}

func TestResolveErrors(t *testing.T) {
	a := newTestAPI(t)
	tok, _ := a.login("lis@example.com", "listener")

	r := a.do("POST", "/tracks/resolve", tok, map[string]string{"input": "https://vimeo.com/123"})
	if r.Status != 400 || errCode(r) != "unsupported_provider" {
		t.Fatalf("unsupported: %d %v", r.Status, r.Body)
	}
	if msg := r.Body["error"].(map[string]any)["message"]; msg != "Unsupported provider: https://vimeo.com" {
		t.Fatalf("message = %v", msg)
	}
	r = a.do("POST", "/tracks/resolve", tok, map[string]string{"input": "song", "provider": "spotify"})
	if r.Status != 400 || errCode(r) != "unknown_provider" {
		t.Fatalf("unknown provider: %d %v", r.Status, r.Body)
	}
	r = a.do("POST", "/tracks/resolve", tok, map[string]string{"input": "https://www.youtube.com/watch?v=missing"})
	if r.Status != 404 || errCode(r) != "track_not_found" {
		t.Fatalf("missing: %d %v", r.Status, r.Body)
	}
	r = a.do("POST", "/tracks/resolve", tok, map[string]string{"input": "https://www.youtube.com/channel/x"})
	if r.Status != 400 || errCode(r) != "invalid_url" {
		t.Fatalf("invalid url: %d %v", r.Status, r.Body)
	}
	r = a.do("POST", "/tracks/resolve", tok, map[string]string{"input": " "})
	if r.Status != 400 || errCode(r) != "invalid_input" {
		t.Fatalf("empty: %d %v", r.Status, r.Body)
	}
	r = a.do("POST", "/tracks/resolve", tok, map[string]string{"input": "lofi beats", "provider": "YouTube"})
	if r.Status != 200 || r.Body["inputKind"] != "search" {
		t.Fatalf("search: %d %v", r.Status, r.Body)
	}
	// Non-JSON body.
	req, _ := http.NewRequest("POST", a.srv.URL+"/tracks/resolve", bytes.NewBufferString("{nope"))
	req.Header.Set("Authorization", "Bearer "+tok)
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != 400 {
		t.Fatalf("bad json: %d", res.StatusCode)
	}
}

func TestUserManagement(t *testing.T) {
	a := newTestAPI(t)
	admin, adminID := a.login("root@example.com", "") // ADMIN_EMAILS grants admin
	mgr, mgrID := a.login("mgr@example.com", "manager")

	if r := a.do("GET", "/users", mgr, nil); r.Status != 403 {
		t.Fatalf("manager list users: %d", r.Status)
	}
	r := a.do("GET", "/users", admin, nil)
	if r.Status != 200 || len(r.Body["users"].([]any)) != 2 {
		t.Fatalf("admin list users: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+mgrID, admin, map[string]string{"role": "listener"}); r.Status != 200 || r.Body["role"] != "listener" {
		t.Fatalf("demote: %d %v", r.Status, r.Body)
	}
	// Role change is effective immediately for the demoted user's existing token.
	if r := a.do("POST", "/playback/pause", mgr, nil); r.Status != 403 {
		t.Fatalf("demoted user still has powers: %d", r.Status)
	}
	if r := a.do("PATCH", "/users/"+adminID, admin, map[string]string{"role": "listener"}); r.Status != 409 {
		t.Fatalf("self-demote: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+mgrID, admin, map[string]string{"role": "god"}); r.Status != 400 || errCode(r) != "invalid_role" {
		t.Fatalf("bad role: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/nobody", admin, map[string]string{"role": "admin"}); r.Status != 404 {
		t.Fatalf("missing user: %d", r.Status)
	}
	if r := a.do("GET", "/roles", mgr, nil); r.Status != 200 || len(r.Body["roles"].([]any)) != 4 {
		t.Fatalf("roles: %d %v", r.Status, r.Body)
	}
}

func TestCookieSessionAndCSRF(t *testing.T) {
	a := newTestAPI(t)
	// Log in and capture the cookie.
	res := a.do("POST", "/auth/dev/login", "", map[string]string{"email": "m@example.com", "role": "manager"})
	var cookie string
	for _, c := range res.Header.Values("Set-Cookie") {
		if len(c) > len(auth.SessionCookie) && c[:len(auth.SessionCookie)] == auth.SessionCookie {
			cookie = c
		}
	}
	if cookie == "" {
		t.Fatal("no session cookie set")
	}
	// Cookie auth works for GET.
	if r := a.do("GET", "/me", "", nil, "Cookie", cookie); r.Status != 200 {
		t.Fatalf("cookie GET: %d %v", r.Status, r.Body)
	}
	// Cookie auth with a foreign Origin on a mutation is rejected.
	if r := a.do("POST", "/playback/pause", "", nil, "Cookie", cookie, "Origin", "https://evil.example"); r.Status != 403 {
		t.Fatalf("csrf: %d %v", r.Status, r.Body)
	}
	// Same-origin from the frontend is fine (409: nothing playing, i.e. it got through).
	if r := a.do("POST", "/playback/pause", "", nil, "Cookie", cookie, "Origin", "http://localhost:3000"); r.Status != 409 {
		t.Fatalf("frontend origin: %d %v", r.Status, r.Body)
	}
	// Logout clears the cookie.
	r := a.do("POST", "/auth/logout", "", nil, "Cookie", cookie, "Origin", "http://localhost:3000")
	if r.Status != 204 {
		t.Fatalf("logout: %d", r.Status)
	}
}

func TestGoogleLoginNotConfigured(t *testing.T) {
	a := newTestAPI(t)
	if r := a.do("GET", "/auth/google/login", "", nil); r.Status != 501 || errCode(r) != "sso_not_configured" {
		t.Fatalf("login: %d %v", r.Status, r.Body)
	}
}
