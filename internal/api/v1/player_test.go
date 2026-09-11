package v1

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"central-station/internal/player"
)

func wsURL(a *testAPI, path string) string {
	return "ws" + strings.TrimPrefix(a.srv.URL, "http") + path
}

func dial(t *testing.T, a *testAPI, path, bearer string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, res, err := websocket.Dial(ctx, wsURL(a, path), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + bearer}},
	})
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", path, err, status)
	}
	t.Cleanup(func() { c.Close(websocket.StatusNormalClosure, "") })
	return c
}

func readServer(t *testing.T, c *websocket.Conn) player.ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m player.ServerMessage
	if err := wsjson.Read(ctx, c, &m); err != nil {
		t.Fatalf("read server message: %v", err)
	}
	return m
}

func writeClient(t *testing.T, c *websocket.Conn, m player.ClientMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, c, m); err != nil {
		t.Fatalf("write client message: %v", err)
	}
}

func readEvent(t *testing.T, c *websocket.Conn) player.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var e player.Event
	if err := wsjson.Read(ctx, c, &e); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return e
}

func TestPlayerSocketAuth(t *testing.T) {
	a := newTestAPI(t)
	ctx := context.Background()
	if _, res, err := websocket.Dial(ctx, wsURL(a, "/player/ws"), nil); err == nil || res == nil || res.StatusCode != 401 {
		t.Fatalf("no token: err=%v res=%v", err, res)
	}
	if _, res, err := websocket.Dial(ctx, wsURL(a, "/player/ws"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer nope"}}}); err == nil || res.StatusCode != 401 {
		t.Fatalf("bad token: err=%v res=%v", err, res)
	}
	if _, res, err := websocket.Dial(ctx, wsURL(a, "/events/ws"), nil); err == nil || res.StatusCode != 401 {
		t.Fatalf("spectator without session: err=%v res=%v", err, res)
	}
}

func TestPlayerDrivesQueue(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("mgr@example.com", "manager")

	// Spectator first: gets an initial idle snapshot.
	spec := dial(t, a, "/events/ws", mgr)
	if ev := readEvent(t, spec); ev.Type != "queue" || ev.Queue.Status != "idle" || ev.Player.Online {
		t.Fatalf("initial event: %+v", ev)
	}

	// Player connects: receives idle desired state; spectator sees it online.
	pl := dial(t, a, "/player/ws", "test-player-token-123")
	if m := readServer(t, pl); m.Type != "state" || m.State.Status != "idle" {
		t.Fatalf("initial state: %+v", m)
	}
	if ev := readEvent(t, spec); ev.Type != "player" || !ev.Player.Online {
		t.Fatalf("player online event: %+v", ev)
	}
	_ = wsjson.Write(context.Background(), pl, player.ClientMessage{Type: "hello", Name: "test-box", Version: "t"})
	if ev := readEvent(t, spec); ev.Type != "player" || ev.Player.Name != "test-box" {
		t.Fatalf("hello event: %+v", ev)
	}
	if r := a.do("GET", "/player", mgr, nil); r.Status != 200 || r.Body["name"] != "test-box" {
		t.Fatalf("GET /player: %d %v", r.Status, r.Body)
	}

	// Manager queues two songs: player gets "playing A", then a second state push.
	r := a.do("POST", "/queue", mgr, map[string]string{"input": "song a"})
	if r.Status != 201 {
		t.Fatalf("add: %d %v", r.Status, r.Body)
	}
	idA := r.Body["item"].(map[string]any)["id"].(string)
	m := readServer(t, pl)
	if m.State.Status != "playing" || m.State.Current == nil || m.State.Current.ID != idA {
		t.Fatalf("state after add: %+v", m.State)
	}
	if ev := readEvent(t, spec); ev.Type != "queue" || ev.Queue.Current.ID != idA {
		t.Fatalf("spectator queue event: %+v", ev)
	}
	r = a.do("POST", "/queue", mgr, map[string]string{"input": "song b"})
	idB := r.Body["item"].(map[string]any)["id"].(string)
	readServer(t, pl) // still A, B queued
	readEvent(t, spec)

	// Progress flows to spectators.
	_ = wsjson.Write(context.Background(), pl, player.ClientMessage{Type: "progress", ItemID: idA, Position: 10, Duration: 200})
	if ev := readEvent(t, spec); ev.Type != "progress" || ev.Player.Position != 10 {
		t.Fatalf("progress event: %+v", ev)
	}

	// Player reports A ended: queue advances to B, player gets the new state.
	_ = wsjson.Write(context.Background(), pl, player.ClientMessage{Type: "ended", ItemID: idA, Reason: "eof"})
	m = readServer(t, pl)
	if m.State.Current == nil || m.State.Current.ID != idB {
		t.Fatalf("state after ended: %+v", m.State)
	}
	ev := readEvent(t, spec)
	if ev.Queue == nil || ev.Queue.Current.ID != idB || len(ev.Queue.Items) != 0 {
		t.Fatalf("queue after ended: %+v", ev)
	}
	// Stale "ended" for A again is ignored but answered with the current state.
	_ = wsjson.Write(context.Background(), pl, player.ClientMessage{Type: "ended", ItemID: idA, Reason: "eof"})
	if m = readServer(t, pl); m.State.Current == nil || m.State.Current.ID != idB {
		t.Fatalf("stale ended changed state: %+v", m.State)
	}

	// Player error on B: recorded in history with reason "error".
	_ = wsjson.Write(context.Background(), pl, player.ClientMessage{Type: "ended", ItemID: idB, Reason: "error", Error: "boom"})
	if m = readServer(t, pl); m.State.Status != "idle" {
		t.Fatalf("state after error: %+v", m.State)
	}
	readEvent(t, spec)
	h := a.do("GET", "/history", mgr, nil)
	entries := h.Body["entries"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["endReason"] != "error" || entries[0].(map[string]any)["error"] != "boom" {
		t.Fatalf("history: %v", entries)
	}

	// Manager pause is pushed to the player.
	a.do("POST", "/queue", mgr, map[string]string{"input": "song c"})
	readServer(t, pl)
	readEvent(t, spec)
	if r := a.do("POST", "/playback/pause", mgr, nil); r.Status != 200 {
		t.Fatalf("pause: %d %v", r.Status, r.Body)
	}
	if m = readServer(t, pl); m.State.Status != "paused" {
		t.Fatalf("pause not pushed: %+v", m.State)
	}

	// Player disconnects: spectators see offline.
	pl.Close(websocket.StatusNormalClosure, "bye")
	readEvent(t, spec) // queue event from pause
	deadline := time.Now().Add(3 * time.Second)
	for {
		ev := readEvent(t, spec)
		if ev.Type == "player" && !ev.Player.Online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never saw player offline")
		}
	}
}
