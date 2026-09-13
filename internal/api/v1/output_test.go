package v1

import (
	"context"
	"testing"

	"github.com/coder/websocket/wsjson"

	"central-station/internal/player"
)

func TestOutputSelection(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	collab, _ := a.login("c@example.com", "collaborator")

	// Defaults: auto, no devices known.
	r := a.do("GET", "/player", collab, nil)
	if r.Status != 200 || r.Body["audioDevice"] != "auto" || len(r.Body["audioDevices"].([]any)) != 0 {
		t.Fatalf("default output: %d %v", r.Status, r.Body)
	}
	// Collaborators cannot switch outputs; managers can.
	if r := a.do("POST", "/playback/output", collab, map[string]string{"device": "auto"}); r.Status != 403 {
		t.Fatalf("collaborator: %d", r.Status)
	}
	if r := a.do("POST", "/playback/output", mgr, map[string]any{}); r.Status != 400 {
		t.Fatalf("missing device: %d %v", r.Status, r.Body)
	}
	// No player connected: any id is accepted and stored.
	if r := a.do("POST", "/playback/output", mgr, map[string]string{"device": "pulse/usb"}); r.Status != 200 || r.Body["audioDevice"] != "pulse/usb" {
		t.Fatalf("offline set: %d %v", r.Status, r.Body)
	}

	spec := dial(t, a, "/events/ws", mgr)
	readEvent(t, spec) // snapshot

	// Player connects and gets the stored choice in its first state.
	p := dial(t, a, "/player/ws", "test-player-token-123")
	if m := readServer(t, p); m.Type != "state" || m.State.AudioDevice != "pulse/usb" {
		t.Fatalf("initial state: %+v", m.State)
	}
	readEvent(t, spec) // online

	// Player reports its outputs; the requested one is missing.
	devs := []player.AudioDevice{{ID: "pulse/bt", Name: "Kitchen speaker"}, {ID: "coreaudio/hp", Name: "Headphones"}}
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "devices", Devices: devs, ActiveDevice: "auto", DeviceMissing: true})
	ev := readEvent(t, spec)
	if ev.Type != "player" || len(ev.Player.AudioDevices) != 2 || !ev.Player.AudioDeviceMissing || ev.Player.ActiveAudioDevice != "auto" {
		t.Fatalf("devices event: %+v", ev.Player)
	}
	r = a.do("GET", "/player", collab, nil)
	if r.Body["audioDeviceMissing"] != true || r.Body["audioDevices"].([]any)[0].(map[string]any)["name"] != "Kitchen speaker" {
		t.Fatalf("status after devices: %v", r.Body)
	}

	// Now that a list is known, unknown ids are rejected with the list attached.
	r = a.do("POST", "/playback/output", mgr, map[string]string{"device": "pulse/nope"})
	if r.Status != 400 || errCode(r) != "unknown_device" {
		t.Fatalf("unknown device: %d %v", r.Status, r.Body)
	}
	if det := r.Body["error"].(map[string]any)["details"].(map[string]any); len(det["audioDevices"].([]any)) != 2 {
		t.Fatalf("details: %v", det)
	}

	// A known id is pushed to the player and to spectators, and clears the missing flag.
	if r := a.do("POST", "/playback/output", mgr, map[string]string{"device": "pulse/bt"}); r.Status != 200 || r.Body["audioDeviceMissing"] == true {
		t.Fatalf("set bt: %d %v", r.Status, r.Body)
	}
	if m := readServer(t, p); m.Type != "state" || m.State.AudioDevice != "pulse/bt" {
		t.Fatalf("player state: %+v", m.State)
	}
	if ev := readEvent(t, spec); ev.Type != "player" || ev.Player.AudioDevice != "pulse/bt" {
		t.Fatalf("spectator: %+v", ev.Player)
	}
	// Player confirms it switched.
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "devices", Devices: devs, ActiveDevice: "pulse/bt"})
	if ev := readEvent(t, spec); ev.Player.ActiveAudioDevice != "pulse/bt" || ev.Player.AudioDeviceMissing {
		t.Fatalf("active: %+v", ev.Player)
	}
	// Back to auto is always allowed.
	if r := a.do("POST", "/playback/output", mgr, map[string]string{"device": "auto"}); r.Status != 200 || r.Body["audioDevice"] != "auto" {
		t.Fatalf("auto: %d %v", r.Status, r.Body)
	}
	readServer(t, p)
}

func TestOutputSurvivesReconnect(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("m@example.com", "manager")
	p := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, p)
	_ = wsjson.Write(context.Background(), p, player.ClientMessage{Type: "devices", Devices: []player.AudioDevice{{ID: "pulse/a", Name: "A"}}, ActiveDevice: "auto"})
	if r := a.do("POST", "/playback/output", mgr, map[string]string{"device": "pulse/a"}); r.Status != 200 {
		t.Fatalf("set: %d %v", r.Status, r.Body)
	}
	readServer(t, p)
	// Player drops and comes back: the choice is still requested, the device
	// list is unknown until it reports again.
	p.CloseNow()
	p2 := dial(t, a, "/player/ws", "test-player-token-123")
	if m := readServer(t, p2); m.State.AudioDevice != "pulse/a" {
		t.Fatalf("state after reconnect: %+v", m.State)
	}
	r := a.do("GET", "/player", mgr, nil)
	if r.Body["audioDevice"] != "pulse/a" || len(r.Body["audioDevices"].([]any)) != 0 {
		t.Fatalf("status after reconnect: %v", r.Body)
	}
}
