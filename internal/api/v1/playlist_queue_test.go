package v1

import "testing"

func TestQueuePlaylists(t *testing.T) {
	a := newTestAPI(t)
	lis, _ := a.login("l@example.com", "listener")
	ann, annID := a.login("ann@example.com", "collaborator")
	bob, _ := a.login("bob@example.com", "collaborator")
	mgr, _ := a.login("m@example.com", "manager")
	plURL := "https://www.youtube.com/playlist?list=PL1"

	if r := a.do("POST", "/queue", lis, map[string]any{"input": plURL}); r.Status != 403 {
		t.Fatalf("listener: %d %v", r.Status, r.Body)
	}
	// Ann queues a playlist while idle: it starts playing, shuffled by default.
	r := a.do("POST", "/queue", ann, map[string]any{"input": plURL})
	if r.Status != 201 || r.Body["kind"] != "playlist" {
		t.Fatalf("add playlist: %d %v", r.Status, r.Body)
	}
	pl := r.Body["playlist"].(map[string]any)
	plID := pl["id"].(string)
	if pl["title"] != "list PL1" || pl["trackCount"].(float64) != 3 || pl["shuffle"] != true || pl["playing"] != true || pl["addedById"] != annID {
		t.Fatalf("playlist summary: %v", pl)
	}
	q := r.Body["queue"].(map[string]any)
	if cur := q["current"].(map[string]any); cur["source"] != "playlist" || cur["playlistId"] != plID {
		t.Fatalf("current: %v", cur)
	}
	if items := q["items"].([]any); len(items) != 0 {
		t.Fatalf("playlist tracks must not be queue items: %v", items)
	}

	// A song queued now plays before the rest of the playlist.
	r = a.do("POST", "/queue", bob, map[string]any{"input": "https://www.youtube.com/watch?v=song1"})
	if r.Status != 201 || r.Body["kind"] != "track" {
		t.Fatalf("add song: %d %v", r.Status, r.Body)
	}
	a.do("POST", "/playback/skip", mgr, nil)
	if q := a.do("GET", "/queue", ann, nil); q.Body["current"].(map[string]any)["track"].(map[string]any)["id"] != "song1" {
		t.Fatalf("song should come first: %v", q.Body["current"])
	}

	// Ownership: Bob cannot touch Ann's playlist; Ann and managers can.
	if r := a.do("PATCH", "/queue/playlists/"+plID, bob, map[string]bool{"shuffle": false}); r.Status != 403 {
		t.Fatalf("bob patch: %d", r.Status)
	}
	if r := a.do("PATCH", "/queue/playlists/"+plID, ann, map[string]bool{"shuffle": false}); r.Status != 200 {
		t.Fatalf("ann patch: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/queue/playlists/"+plID, mgr, map[string]any{}); r.Status != 400 {
		t.Fatalf("missing shuffle: %d", r.Status)
	}
	if q := a.do("GET", "/queue", ann, nil); q.Body["playlists"].([]any)[0].(map[string]any)["shuffle"] != false {
		t.Fatalf("shuffle not updated: %v", q.Body["playlists"])
	}
	if r := a.do("DELETE", "/queue/playlists/"+plID, bob, nil); r.Status != 403 {
		t.Fatalf("bob delete: %d", r.Status)
	}
	if r := a.do("DELETE", "/queue/playlists/nope", mgr, nil); r.Status != 404 || errCode(r) != "playlist_not_found" {
		t.Fatalf("unknown delete: %d %v", r.Status, r.Body)
	}
	if r := a.do("DELETE", "/queue/playlists/"+plID, mgr, nil); r.Status != 200 {
		t.Fatalf("manager delete: %d %v", r.Status, r.Body)
	}
	if q := a.do("GET", "/queue", ann, nil); len(q.Body["playlists"].([]any)) != 0 {
		t.Fatalf("playlist not removed: %v", q.Body["playlists"])
	}

	// Errors: unsupported host, and a real resolver failure is not mistaken for a playlist.
	if r := a.do("POST", "/queue", ann, map[string]any{"input": "https://vimeo.com/123"}); r.Status != 400 || errCode(r) != "unsupported_provider" {
		t.Fatalf("unsupported: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/queue", ann, map[string]any{"input": "https://www.youtube.com/watch?v=boom"}); r.Status != 502 {
		t.Fatalf("provider failure: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/queue", ann, map[string]any{"input": "https://www.youtube.com/channel/abc"}); r.Status != 400 || errCode(r) != "invalid_url" {
		t.Fatalf("channel url: %d %v", r.Status, r.Body)
	}
}
