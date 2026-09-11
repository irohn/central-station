package v1

import "testing"

func TestVolumeEndpoint(t *testing.T) {
	a := newTestAPI(t)
	collab, _ := a.login("c@example.com", "collaborator")
	mgr, _ := a.login("m@example.com", "manager")

	if r := a.do("GET", "/player", collab, nil); r.Status != 200 || r.Body["volume"].(float64) != 80 {
		t.Fatalf("default volume: %d %v", r.Status, r.Body)
	}
	lis, _ := a.login("l@example.com", "listener")
	if r := a.do("POST", "/playback/volume", lis, map[string]int{"volume": 50}); r.Status != 403 {
		t.Fatalf("listener: %d", r.Status)
	}
	if r := a.do("POST", "/playback/volume", collab, map[string]int{"volume": 50}); r.Status != 200 {
		t.Fatalf("collaborator: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/volume", mgr, map[string]int{"volume": 150}); r.Status != 400 || errCode(r) != "invalid_volume" {
		t.Fatalf("out of range: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/volume", mgr, map[string]any{}); r.Status != 400 {
		t.Fatalf("missing: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/volume", mgr, map[string]int{"volume": 55}); r.Status != 200 || r.Body["volume"].(float64) != 55 {
		t.Fatalf("set: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/player", collab, nil); r.Body["volume"].(float64) != 55 {
		t.Fatalf("read back: %v", r.Body)
	}
}

func TestVolumeReachesPlayerAndSpectators(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	spec := dial(t, a, "/events/ws", mgr)
	readEvent(t, spec) // initial snapshot
	p := dial(t, a, "/player/ws", "test-player-token-123")
	if m := readServer(t, p); m.Type != "state" || m.State.Volume != 80 {
		t.Fatalf("initial state: %+v", m)
	}
	readEvent(t, spec) // player online

	if r := a.do("POST", "/playback/volume", mgr, map[string]int{"volume": 30}); r.Status != 200 {
		t.Fatalf("set: %d %v", r.Status, r.Body)
	}
	if m := readServer(t, p); m.Type != "state" || m.State.Volume != 30 {
		t.Fatalf("player did not get the new volume: %+v", m)
	}
	if ev := readEvent(t, spec); ev.Type != "player" || ev.Player.Volume != 30 {
		t.Fatalf("spectator did not get the new volume: %+v", ev)
	}
}
