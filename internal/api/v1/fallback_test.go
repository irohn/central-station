package v1

import (
	"testing"
)

func TestFallbackSettingsEndpoints(t *testing.T) {
	a := newTestAPI(t)
	collab, _ := a.login("c@example.com", "collaborator")
	mgr, _ := a.login("m@example.com", "manager")

	// Readable by everyone, editable by managers only.
	if r := a.do("GET", "/settings/fallback", collab, nil); r.Status != 200 || r.Body["enabled"] != false {
		t.Fatalf("get: %d %v", r.Status, r.Body)
	}
	if r := a.do("PUT", "/settings/fallback", collab, map[string]any{"url": "https://www.youtube.com/playlist?list=PL1", "enabled": true}); r.Status != 403 {
		t.Fatalf("collaborator put: %d %v", r.Status, r.Body)
	}
	if r := a.do("PUT", "/settings/fallback", mgr, map[string]any{"url": "https://www.youtube.com/watch?v=abc", "enabled": true}); r.Status != 400 || errCode(r) != "not_a_playlist" {
		t.Fatalf("not a playlist: %d %v", r.Status, r.Body)
	}
	if r := a.do("PUT", "/settings/fallback", mgr, map[string]any{"enabled": true}); r.Status != 400 || errCode(r) != "invalid_input" {
		t.Fatalf("missing url: %d %v", r.Status, r.Body)
	}

	// Enabling while idle starts the playlist immediately.
	r := a.do("PUT", "/settings/fallback", mgr, map[string]any{"url": "https://www.youtube.com/playlist?list=PL1", "enabled": true, "shuffle": false})
	if r.Status != 200 {
		t.Fatalf("put: %d %v", r.Status, r.Body)
	}
	pl := r.Body["playlist"].(map[string]any)
	if pl["title"] != "list PL1" || pl["trackCount"].(float64) != 3 {
		t.Fatalf("playlist: %v", pl)
	}
	q := a.do("GET", "/queue", collab, nil)
	cur := q.Body["current"].(map[string]any)
	if q.Body["status"] != "playing" || cur["source"] != "fallback" || cur["track"].(map[string]any)["id"] != "PL1-0" {
		t.Fatalf("queue after enable: %v", q.Body)
	}
	fb := q.Body["fallback"].(map[string]any)
	if fb["enabled"] != true || fb["next"].(map[string]any)["id"] != "PL1-1" || fb["playlistTitle"] != "list PL1" {
		t.Fatalf("snapshot fallback: %v", fb)
	}
	if items := q.Body["items"].([]any); len(items) != 0 {
		t.Fatalf("fallback track must not appear in the user queue: %v", items)
	}

	// Switching playlists skips the playing fallback track.
	r = a.do("PUT", "/settings/fallback", mgr, map[string]any{"url": "https://www.youtube.com/playlist?list=PL2", "enabled": true})
	if r.Status != 200 {
		t.Fatalf("switch: %d %v", r.Status, r.Body)
	}
	q = a.do("GET", "/queue", collab, nil)
	if id := q.Body["current"].(map[string]any)["track"].(map[string]any)["id"]; id != "PL2-0" {
		t.Fatalf("after switch: %v", id)
	}
	h := a.do("GET", "/history", collab, nil)
	if entries := h.Body["entries"].([]any); len(entries) != 1 || entries[0].(map[string]any)["endReason"] != "skipped" {
		t.Fatalf("history: %v", h.Body)
	}

	// Disabling stops the fallback track and leaves the player idle.
	if r := a.do("PUT", "/settings/fallback", mgr, map[string]any{"url": "https://www.youtube.com/playlist?list=PL2", "enabled": false}); r.Status != 200 {
		t.Fatalf("disable: %d %v", r.Status, r.Body)
	}
	if q := a.do("GET", "/queue", collab, nil); q.Body["status"] != "idle" || q.Body["current"] != nil {
		t.Fatalf("after disable: %v", q.Body)
	}
	if r := a.do("POST", "/settings/fallback/refresh", mgr, nil); r.Status != 200 {
		t.Fatalf("refresh: %d %v", r.Status, r.Body)
	}
}

func TestFallbackReset(t *testing.T) {
	// Without a server default, reset is refused and envDefault is absent.
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	if r := a.do("GET", "/settings/fallback", mgr, nil); r.Body["envDefault"] != nil {
		t.Fatalf("envDefault without config: %v", r.Body)
	}
	if r := a.do("POST", "/settings/fallback/reset", mgr, nil); r.Status != 409 || errCode(r) != "no_default_configured" {
		t.Fatalf("reset without config: %d %v", r.Status, r.Body)
	}

	// With FALLBACK_PLAYLIST_URL set, reset restores it after a manager changed it.
	b := newTestAPIWith(t, map[string]string{"FALLBACK_PLAYLIST_URL": "https://www.youtube.com/playlist?list=PLdefault", "FALLBACK_SHUFFLE": "false"})
	mgr2, _ := b.login("m@example.com", "manager")
	r := b.do("GET", "/settings/fallback", mgr2, nil)
	env, _ := r.Body["envDefault"].(map[string]any)
	if env == nil || env["url"] != "https://www.youtube.com/playlist?list=PLdefault" || env["shuffle"] != false {
		t.Fatalf("envDefault: %v", r.Body)
	}
	if r := b.do("PUT", "/settings/fallback", mgr2, map[string]any{"url": "https://www.youtube.com/playlist?list=PLother", "enabled": true, "shuffle": true}); r.Status != 200 {
		t.Fatalf("change: %d %v", r.Status, r.Body)
	}
	r = b.do("POST", "/settings/fallback/reset", mgr2, nil)
	if r.Status != 200 || r.Body["url"] != "https://www.youtube.com/playlist?list=PLdefault" || r.Body["enabled"] != true || r.Body["shuffle"] != false {
		t.Fatalf("reset: %d %v", r.Status, r.Body)
	}
	col, _ := b.login("c@example.com", "collaborator")
	if r := b.do("POST", "/settings/fallback/reset", col, nil); r.Status != 403 {
		t.Fatalf("collaborator reset: %d", r.Status)
	}
}
