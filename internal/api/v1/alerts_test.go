package v1

import (
	"testing"

	"central-station/internal/clips"
	"central-station/internal/player"
)

func TestAlertsEndpoints(t *testing.T) {
	a := newTestAPI(t)
	collab, _ := a.login("c@example.com", "collaborator")
	mgr, _ := a.login("m@example.com", "manager")
	clip := a.upload(mgr, "Lunch", "recording", "audio/mp4", []byte("mp4"), "2")
	clipID := clip.Body["id"].(string)

	if r := a.do("GET", "/alerts", collab, nil); r.Status != 403 {
		t.Fatalf("collaborator: %d", r.Status)
	}
	if r := a.do("GET", "/alerts", mgr, nil); r.Status != 200 || len(r.Body["alerts"].([]any)) != 0 || r.Body["defaultTimezone"] != "Asia/Jerusalem" {
		t.Fatalf("empty list: %d %v", r.Status, r.Body)
	}

	// Validation.
	for name, body := range map[string]map[string]any{
		"no name":      {"schedule": "0 12 * * *", "clipId": clipID},
		"bad cron":     {"name": "x", "schedule": "0 12 * *", "clipId": clipID},
		"bad tz":       {"name": "x", "schedule": "0 12 * * *", "clipId": clipID, "timezone": "Nope/Nowhere"},
		"bad loops":    {"name": "x", "schedule": "0 12 * * *", "clipId": clipID, "loops": 99},
		"unknown type": {"name": "x", "schedule": "0 12 * * *", "clipId": clipID, "type": "event"},
	} {
		if r := a.do("POST", "/alerts", mgr, body); r.Status != 400 || errCode(r) != "invalid_alert" {
			t.Fatalf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	if r := a.do("POST", "/alerts", mgr, map[string]any{"name": "x", "schedule": "0 12 * * *", "clipId": "00000000-0000-4000-8000-000000000000"}); r.Status != 404 {
		t.Fatalf("missing clip: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/alerts", mgr, map[string]any{"name": "x", "schedule": "0 12 * * *", "clipId": clipID, "chimeId": clipID}); r.Status != 400 || errCode(r) != "not_a_chime" {
		t.Fatalf("non-chime chime: %d %v", r.Status, r.Body)
	}

	// Create: defaults fill in, next fire is computed.
	r := a.do("POST", "/alerts", mgr, map[string]any{"name": "Lunch call", "schedule": "0 12 * * 1-5", "clipId": clipID, "loops": 2})
	if r.Status != 201 || r.Body["enabled"] != true || r.Body["type"] != "cron" || r.Body["timezone"] != "Asia/Jerusalem" || r.Body["loops"].(float64) != 2 || r.Body["nextFireAt"] == nil {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	id := r.Body["id"].(string)

	// Patch a single field; disabling clears the next fire time.
	if r := a.do("PATCH", "/alerts/"+id, mgr, map[string]any{"enabled": false}); r.Status != 200 || r.Body["enabled"] != false || r.Body["nextFireAt"] != nil || r.Body["name"] != "Lunch call" {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/alerts/"+id, mgr, map[string]any{"schedule": "bogus"}); r.Status != 400 {
		t.Fatalf("patch bad schedule: %d", r.Status)
	}
	if r := a.do("PATCH", "/alerts/nope", mgr, map[string]any{"enabled": true}); r.Status != 404 || errCode(r) != "alert_not_found" {
		t.Fatalf("patch unknown: %d %v", r.Status, r.Body)
	}

	// Test-fire goes to the player with the default chime.
	if r := a.do("POST", "/alerts/"+id+"/test", mgr, nil); r.Status != 409 || errCode(r) != "player_offline" {
		t.Fatalf("test offline: %d %v", r.Status, r.Body)
	}
	p := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, p)
	if r := a.do("POST", "/alerts/"+id+"/test", mgr, nil); r.Status != 202 {
		t.Fatalf("test: %d %v", r.Status, r.Body)
	}
	if m := readServer(t, p); m.Type != "broadcast" || m.Broadcast.Loops != 2 || m.Broadcast.ChimeURL != "/api/v1/player/clips/"+clips.DefaultChimeID+"/audio" || m.Broadcast.DuckPercent != player.DuckPercent {
		t.Fatalf("player command: %+v %+v", m, m.Broadcast)
	}

	if r := a.do("DELETE", "/alerts/"+id, mgr, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if r := a.do("GET", "/alerts", mgr, nil); len(r.Body["alerts"].([]any)) != 0 {
		t.Fatalf("after delete: %v", r.Body)
	}
}
