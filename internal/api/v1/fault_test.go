package v1

import (
	"context"
	"testing"

	"github.com/coder/websocket/wsjson"

	"central-station/internal/player"
)

func TestOutputFaultPausesAndRecovers(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	p := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, p)

	r := a.do("POST", "/queue", mgr, map[string]string{"input": "song a"})
	idA := r.Body["item"].(map[string]any)["id"].(string)
	a.do("POST", "/queue", mgr, map[string]string{"input": "song b"})
	readServer(t, p) // playing A
	readServer(t, p) // B queued

	// Player cannot open its output: the zone pauses on A, nothing is skipped.
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "output_error", ItemID: idA, Error: "audio output initialization failed"})
	m := readServer(t, p)
	if m.State.Status != "paused" || m.State.Current == nil || m.State.Current.ID != idA {
		t.Fatalf("expected paused on A, got %+v", m.State)
	}
	st := a.do("GET", "/player", mgr, nil)
	fault, _ := st.Body["fault"].(map[string]any)
	if fault == nil || fault["kind"] != "output" {
		t.Fatalf("fault not reported: %v", st.Body)
	}
	if h := a.do("GET", "/history", mgr, nil); len(h.Body["entries"].([]any)) != 0 {
		t.Fatalf("history should be empty, got %v", h.Body)
	}
	// Repeated failures while held change nothing (no skip, no new state).
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "output_error", ItemID: idA, Error: "audio output initialization failed"})
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "ended", ItemID: idA, Reason: "error", Error: "audio output initialization failed"})
	if q := a.do("GET", "/queue", mgr, nil); q.Body["status"] != "paused" || q.Body["current"].(map[string]any)["id"] != idA || len(q.Body["items"].([]any)) != 1 {
		t.Fatalf("queue moved while held: %v", q.Body)
	}

	// Output comes back: the zone resumes on A and the fault clears.
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "output_ok"})
	m = readServer(t, p)
	if m.State.Status != "playing" || m.State.Current.ID != idA {
		t.Fatalf("expected resume on A, got %+v", m.State)
	}
	if st := a.do("GET", "/player", mgr, nil); st.Body["fault"] != nil {
		t.Fatalf("fault should be cleared: %v", st.Body["fault"])
	}
}

func TestOutputFaultManualResumeStaysHeldUntilFixed(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	p := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, p)
	r := a.do("POST", "/queue", mgr, map[string]string{"input": "song a"})
	idA := r.Body["item"].(map[string]any)["id"].(string)
	readServer(t, p)
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "output_error", ItemID: idA, Error: "audio output initialization failed"})
	readServer(t, p) // paused

	// A manager resumes by hand; the player still fails -> paused again, still on A.
	if r := a.do("POST", "/playback/resume", mgr, nil); r.Status != 200 {
		t.Fatalf("resume: %d %v", r.Status, r.Body)
	}
	readServer(t, p) // playing
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "output_error", ItemID: idA, Error: "audio output initialization failed"})
	if m := readServer(t, p); m.State.Status != "paused" || m.State.Current.ID != idA {
		t.Fatalf("expected re-pause on A: %+v", m.State)
	}

	// A player reconnect (e.g. node rebooted with speakers back) resumes automatically.
	p.CloseNow()
	p2 := dial(t, a, "/player/ws", "test-player-token-123")
	if m := readServer(t, p2); m.State.Status != "playing" || m.State.Current.ID != idA {
		t.Fatalf("expected resume on reconnect: %+v", m.State)
	}
	if st := a.do("GET", "/player", mgr, nil); st.Body["fault"] != nil {
		t.Fatalf("fault should be cleared on reconnect: %v", st.Body["fault"])
	}
}

func TestConsecutiveTrackErrorsPause(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	p := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, p)
	var ids []string
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		r := a.do("POST", "/queue", mgr, map[string]string{"input": "song " + s})
		ids = append(ids, r.Body["item"].(map[string]any)["id"].(string))
		readServer(t, p)
	}
	fail := func(id string) {
		_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "ended", ItemID: id, Reason: "error", Error: "yt-dlp failed"})
	}
	// Three failures: a, b, c are skipped (history) and the zone pauses on d.
	fail(ids[0])
	if m := readServer(t, p); m.State.Current.ID != ids[1] {
		t.Fatalf("after 1st: %+v", m.State.Current)
	}
	fail(ids[1])
	readServer(t, p)
	fail(ids[2])
	m := readServer(t, p) // advanced to d (playing)
	if m.State.Current.ID != ids[3] {
		t.Fatalf("after 3rd: %+v", m.State.Current)
	}
	m = readServer(t, p) // then paused
	if m.State.Status != "paused" || m.State.Current.ID != ids[3] {
		t.Fatalf("expected pause on d: %+v", m.State)
	}
	st := a.do("GET", "/player", mgr, nil)
	if f, _ := st.Body["fault"].(map[string]any); f == nil || f["kind"] != "playback" {
		t.Fatalf("playback fault missing: %v", st.Body)
	}
	h := a.do("GET", "/history", mgr, nil)
	if n := len(h.Body["entries"].([]any)); n != 3 {
		t.Fatalf("history entries = %d, want 3", n)
	}
	// While held, another failure report does not skip d.
	fail(ids[3])
	if q := a.do("GET", "/queue", mgr, nil); q.Body["current"].(map[string]any)["id"] != ids[3] || len(q.Body["items"].([]any)) != 1 {
		t.Fatalf("held queue moved: %v", q.Body)
	}
	// Manager fixes things and resumes; d plays for a while -> fault cleared, streak reset.
	a.do("POST", "/playback/resume", mgr, nil)
	readServer(t, p)
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "progress", ItemID: ids[3], Position: 5, Duration: 100})
	deadline := 50
	for {
		st = a.do("GET", "/player", mgr, nil)
		if st.Body["fault"] == nil {
			break
		}
		if deadline--; deadline == 0 {
			t.Fatalf("fault not cleared after progress: %v", st.Body)
		}
	}
	// A single failure afterwards skips normally again (streak was reset).
	fail(ids[3])
	if m := readServer(t, p); m.State.Status != "playing" || m.State.Current.ID != ids[4] {
		t.Fatalf("after reset: %+v", m.State)
	}
}
