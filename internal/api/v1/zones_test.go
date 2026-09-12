package v1

import (
	"context"
	"net/http"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"central-station/internal/player"
	"central-station/internal/zone"
)

func TestZonesCRUDAndIsolation(t *testing.T) {
	a := newTestAPI(t)
	admin, _ := a.login("root@example.com", "")
	mgr, _ := a.login("mgr@example.com", "manager")

	// Default zone exists and legacy unscoped routes address it.
	r := a.do("GET", "/zones", mgr, nil)
	if r.Status != 200 || len(r.Body["zones"].([]any)) != 1 || r.Body["defaultId"] != zone.DefaultID {
		t.Fatalf("list zones: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/zones", mgr, map[string]string{"name": "Kitchen"}); r.Status != 403 {
		t.Fatalf("manager create zone: %d", r.Status)
	}
	if r := a.do("POST", "/zones", admin, map[string]string{"name": "  "}); r.Status != 400 || errCode(r) != "invalid_zone" {
		t.Fatalf("blank name: %d %v", r.Status, r.Body)
	}
	r = a.do("POST", "/zones", admin, map[string]string{"name": "Kitchen"})
	if r.Status != 201 {
		t.Fatalf("create zone: %d %v", r.Status, r.Body)
	}
	kitchen := r.Body["zone"].(map[string]any)
	kid := kitchen["id"].(string)
	ktoken := r.Body["token"].(string)
	if kid == "" || ktoken == "" || kitchen["hasToken"] != true {
		t.Fatalf("zone/token missing: %v", r.Body)
	}

	// Queue something in each zone; they must not see each other.
	if r := a.do("POST", "/queue", mgr, map[string]string{"input": "office song"}); r.Status != 201 {
		t.Fatalf("add default: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/zones/"+kid+"/queue", mgr, map[string]string{"input": "kitchen song"}); r.Status != 201 {
		t.Fatalf("add kitchen: %d %v", r.Status, r.Body)
	}
	def := a.do("GET", "/zones/"+zone.DefaultID+"/queue", mgr, nil)
	kq := a.do("GET", "/zones/"+kid+"/queue", mgr, nil)
	legacy := a.do("GET", "/queue", mgr, nil)
	title := func(r resp) string {
		return r.Body["current"].(map[string]any)["track"].(map[string]any)["title"].(string)
	}
	if title(def) != "result for office song" || title(kq) != "result for kitchen song" || title(legacy) != title(def) {
		t.Fatalf("isolation broken: default=%q kitchen=%q legacy=%q", title(def), title(kq), title(legacy))
	}
	if r := a.do("GET", "/zones/nope/queue", mgr, nil); r.Status != 404 || errCode(r) != "zone_not_found" {
		t.Fatalf("unknown zone: %d %v", r.Status, r.Body)
	}

	// Per-zone volume.
	if r := a.do("POST", "/zones/"+kid+"/playback/volume", mgr, map[string]int{"volume": 33}); r.Status != 200 {
		t.Fatalf("kitchen volume: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/player", mgr, nil); r.Body["volume"].(float64) != 80 {
		t.Fatalf("default volume changed: %v", r.Body["volume"])
	}

	// Summaries reflect both zones.
	r = a.do("GET", "/zones", mgr, nil)
	zs := r.Body["zones"].([]any)
	if len(zs) != 2 || zs[1].(map[string]any)["name"] != "Kitchen" || zs[1].(map[string]any)["queued"].(float64) != 0 || zs[1].(map[string]any)["current"] == nil {
		t.Fatalf("summaries: %v", zs)
	}

	// Rename and delete guards.
	if r := a.do("PATCH", "/zones/"+kid, admin, map[string]string{"name": "Kitchen 2"}); r.Status != 200 || r.Body["name"] != "Kitchen 2" {
		t.Fatalf("rename: %d %v", r.Status, r.Body)
	}
	if r := a.do("DELETE", "/zones/"+kid, mgr, nil); r.Status != 403 {
		t.Fatalf("manager delete: %d", r.Status)
	}
	if r := a.do("DELETE", "/zones/"+kid, admin, nil); r.Status != 204 {
		t.Fatalf("delete: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/zones/"+kid+"/queue", mgr, nil); r.Status != 404 {
		t.Fatalf("deleted zone still served: %d", r.Status)
	}
	if r := a.do("DELETE", "/zones/"+zone.DefaultID, admin, nil); r.Status != 409 || errCode(r) != "last_zone" {
		t.Fatalf("delete last zone: %d %v", r.Status, r.Body)
	}
}

func TestZonePlayerTokens(t *testing.T) {
	a := newTestAPI(t)
	admin, _ := a.login("root@example.com", "")
	mgr, _ := a.login("mgr@example.com", "manager")
	r := a.do("POST", "/zones", admin, map[string]string{"name": "Kitchen"})
	kid := r.Body["zone"].(map[string]any)["id"].(string)
	ktoken := r.Body["token"].(string)

	// Legacy PLAYER_TOKEN drives the default zone; the new token drives Kitchen.
	defPlayer := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, defPlayer)
	kPlayer := dial(t, a, "/player/ws", ktoken)
	readServer(t, kPlayer)
	_ = wsjson.Write(context.Background(), kPlayer, player.ClientMessage{Type: "hello", Name: "kitchen-pi"})

	ctx := context.Background()
	if _, res, err := websocket.Dial(ctx, wsURL(a, "/player/ws"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer csp_wrong"}}}); err == nil || res == nil || res.StatusCode != 401 {
		t.Fatalf("bad token accepted: %v", res)
	}

	deadline := 20
	for {
		r = a.do("GET", "/zones", mgr, nil)
		zs := r.Body["zones"].([]any)
		k := zs[1].(map[string]any)["player"].(map[string]any)
		d := zs[0].(map[string]any)["player"].(map[string]any)
		if k["online"] == true && k["name"] == "kitchen-pi" && d["online"] == true {
			break
		}
		if deadline--; deadline == 0 {
			t.Fatalf("players not reflected in zones: %v", zs)
		}
	}

	// A song queued in Kitchen reaches only the Kitchen player.
	if r := a.do("POST", "/zones/"+kid+"/queue", mgr, map[string]string{"input": "kitchen song"}); r.Status != 201 {
		t.Fatalf("add: %d %v", r.Status, r.Body)
	}
	if m := readServer(t, kPlayer); m.State.Current == nil || m.State.Current.Track.Title != "result for kitchen song" {
		t.Fatalf("kitchen player state: %+v", m.State)
	}
	rctx, cancel := context.WithTimeout(ctx, 300_000_000) // 300ms: nothing should arrive
	var stray player.ServerMessage
	if err := wsjson.Read(rctx, defPlayer, &stray); err == nil {
		cancel()
		t.Fatalf("default player received kitchen state: %+v", stray)
	}
	cancel()

	// Rotating the token disconnects the old player and the old token stops working.
	r = a.do("POST", "/zones/"+kid+"/token", admin, nil)
	if r.Status != 200 || r.Body["token"] == ktoken {
		t.Fatalf("rotate: %d %v", r.Status, r.Body)
	}
	if _, res, err := websocket.Dial(ctx, wsURL(a, "/player/ws"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + ktoken}}}); err == nil || res.StatusCode != 401 {
		t.Fatalf("old token still works: %v", res)
	}
	newTok := r.Body["token"].(string)
	kPlayer2 := dial(t, a, "/player/ws", newTok)
	if m := readServer(t, kPlayer2); m.State.Current == nil {
		t.Fatalf("new token player got no state: %+v", m)
	}
}

func TestBroadcastAllZones(t *testing.T) {
	a := newTestAPI(t)
	admin, _ := a.login("root@example.com", "")
	r := a.do("POST", "/zones", admin, map[string]string{"name": "Kitchen"})
	kid := r.Body["zone"].(map[string]any)["id"].(string)
	ktoken := r.Body["token"].(string)

	clipsRes := a.do("GET", "/clips", admin, nil)
	chime := clipsRes.Body["defaultChimeId"].(string)

	// Nobody online anywhere: 409.
	if r := a.do("POST", "/broadcasts", admin, map[string]any{"clipId": chime, "chimeId": "none", "allZones": true}); r.Status != 409 {
		t.Fatalf("all zones offline: %d %v", r.Status, r.Body)
	}
	kPlayer := dial(t, a, "/player/ws", ktoken)
	readServer(t, kPlayer)

	// Zone-scoped to the default zone (offline) fails; all-zones reaches Kitchen.
	if r := a.do("POST", "/broadcasts", admin, map[string]any{"clipId": chime, "chimeId": "none"}); r.Status != 409 {
		t.Fatalf("default zone offline: %d %v", r.Status, r.Body)
	}
	r = a.do("POST", "/broadcasts", admin, map[string]any{"clipId": chime, "chimeId": "none", "allZones": true})
	if r.Status != 202 || len(r.Body["zones"].([]any)) != 1 || r.Body["zones"].([]any)[0] != "Kitchen" {
		t.Fatalf("all zones: %d %v", r.Status, r.Body)
	}
	if m := readServer(t, kPlayer); m.Type != "broadcast" || m.Broadcast == nil {
		t.Fatalf("kitchen player did not get the broadcast: %+v", m)
	}
	// Zone-scoped route to Kitchen works too.
	if r := a.do("POST", "/zones/"+kid+"/broadcasts", admin, map[string]any{"clipId": chime, "chimeId": "none"}); r.Status != 202 {
		t.Fatalf("kitchen broadcast: %d %v", r.Status, r.Body)
	}
}
