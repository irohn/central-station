package v1

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"central-station/internal/clips"
	"central-station/internal/player"
)

func (a *testAPI) upload(token, name, kind, mime string, data []byte, dur string) resp {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", name)
	_ = mw.WriteField("kind", kind)
	_ = mw.WriteField("durationSeconds", dur)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="clip"`}
	hdr["Content-Type"] = []string{mime}
	part, _ := mw.CreatePart(hdr)
	_, _ = part.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", a.srv.URL+"/clips", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	out := resp{Status: res.StatusCode, Header: res.Header}
	_ = json.NewDecoder(res.Body).Decode(&out.Body)
	return out
}

func TestClipsAndBroadcast(t *testing.T) {
	a := newTestAPI(t)
	collab, _ := a.login("c@example.com", "collaborator")
	mgr, mgrID := a.login("m@example.com", "manager")
	admin, _ := a.login("root@example.com", "admin")

	// Bundled chimes are listed; collaborators cannot see or send.
	if r := a.do("GET", "/clips", collab, nil); r.Status != 403 {
		t.Fatalf("collaborator list: %d", r.Status)
	}
	r := a.do("GET", "/clips", mgr, nil)
	if r.Status != 200 || len(r.Body["clips"].([]any)) != 3 || r.Body["defaultChimeId"] != clips.DefaultChimeID || r.Body["ttsAvailable"] != false {
		t.Fatalf("list: %d %v", r.Status, r.Body)
	}

	// Upload validation.
	if r := a.upload(mgr, "x", "recording", "text/plain", []byte("nope"), "1"); r.Status != 400 || errCode(r) != "unsupported_audio" {
		t.Fatalf("bad mime: %d %v", r.Status, r.Body)
	}
	if r := a.upload(mgr, "x", "recording", "audio/mp4", []byte("aaaa"), "99"); r.Status != 400 || errCode(r) != "too_long" {
		t.Fatalf("too long: %d %v", r.Status, r.Body)
	}
	r = a.upload(mgr, "Lunch", "recording", "audio/webm;codecs=opus", []byte("webm-bytes"), "2.5")
	if r.Status != 201 || r.Body["mime"] != "audio/webm" || r.Body["kind"] != "recording" || r.Body["createdById"] != mgrID {
		t.Fatalf("upload: %d %v", r.Status, r.Body)
	}
	clipID := r.Body["id"].(string)

	// Audio is served to browsers and to the player (token auth).
	req, _ := http.NewRequest("GET", a.srv.URL+"/clips/"+clipID+"/audio", nil)
	req.Header.Set("Authorization", "Bearer "+collab)
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/webm" {
		t.Fatalf("browser audio: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	res.Body.Close()
	req, _ = http.NewRequest("GET", a.srv.URL+"/player/clips/"+clipID+"/audio", nil)
	req.Header.Set("Authorization", "Bearer test-player-token-123")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 200 {
		t.Fatalf("player audio: %d", res.StatusCode)
	}
	res.Body.Close()
	if r := a.do("GET", "/player/clips/"+clipID+"/audio", "", nil); r.Status != 401 {
		t.Fatalf("player audio without token: %d", r.Status)
	}

	// TTS is off in tests.
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "hi"}); r.Status != 503 {
		t.Fatalf("tts: %d %v", r.Status, r.Body)
	}

	// Broadcast needs a player.
	if r := a.do("POST", "/broadcasts", mgr, map[string]any{"clipId": clipID}); r.Status != 409 || errCode(r) != "player_offline" {
		t.Fatalf("offline: %d %v", r.Status, r.Body)
	}
	p := dial(t, a, "/player/ws", "test-player-token-123")
	readServer(t, p) // initial state
	spec := dial(t, a, "/events/ws", collab)
	readEvent(t, spec)

	if r := a.do("POST", "/broadcasts", mgr, map[string]any{"clipId": clipID, "loops": 99}); r.Status != 400 {
		t.Fatalf("loops: %d", r.Status)
	}
	r = a.do("POST", "/broadcasts", mgr, map[string]any{"clipId": clipID, "loops": 2})
	if r.Status != 202 || r.Body["position"].(float64) != 0 {
		t.Fatalf("send: %d %v", r.Status, r.Body)
	}
	bcID := r.Body["id"].(string)
	m := readServer(t, p)
	if m.Type != "broadcast" || m.Broadcast.ID != bcID || m.Broadcast.Loops != 2 || m.Broadcast.ClipURL != "/api/v1/player/clips/"+clipID+"/audio" || m.Broadcast.ChimeURL != "/api/v1/player/clips/"+clips.DefaultChimeID+"/audio" || m.Broadcast.DuckPercent != player.DuckPercent {
		t.Fatalf("player command: %+v %+v", m, m.Broadcast)
	}
	// Browsers see it in the player status; a second one queues behind.
	found := false
	for i := 0; i < 3 && !found; i++ {
		ev := readEvent(t, spec)
		found = ev.Player != nil && ev.Player.Broadcast != nil && ev.Player.Broadcast.ID == bcID && ev.Player.Broadcast.ClipName == "Lunch"
	}
	if !found {
		t.Fatal("spectators never saw the broadcast")
	}
	r = a.do("POST", "/broadcasts", mgr, map[string]any{"clipId": clipID, "chimeId": "none"})
	if r.Status != 202 || r.Body["position"].(float64) != 1 {
		t.Fatalf("second send: %d %v", r.Status, r.Body)
	}
	if g := a.do("GET", "/broadcasts", collab, nil); len(g.Body["queued"].([]any)) != 1 {
		t.Fatalf("queue: %v", g.Body)
	}
	// Player finishes the first: the second is dispatched, without a chime.
	writeClient(t, p, player.ClientMessage{Type: "broadcast_ended", BroadcastID: bcID, Reason: "done"})
	if m := readServer(t, p); m.Type != "broadcast" || m.Broadcast.ChimeURL != "" || m.Broadcast.Loops != 1 {
		t.Fatalf("second command: %+v %+v", m, m.Broadcast)
	}

	// Default chime: admins only, chimes only.
	if r := a.do("PUT", "/settings/chime", mgr, map[string]string{"clipId": clips.ChimeBellID}); r.Status != 403 {
		t.Fatalf("mgr chime: %d", r.Status)
	}
	if r := a.do("PUT", "/settings/chime", admin, map[string]string{"clipId": clipID}); r.Status != 400 {
		t.Fatalf("non-chime default: %d", r.Status)
	}
	if r := a.do("PUT", "/settings/chime", admin, map[string]string{"clipId": clips.ChimeBellID}); r.Status != 200 {
		t.Fatalf("set chime: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/clips", mgr, nil); r.Body["defaultChimeId"] != clips.ChimeBellID {
		t.Fatalf("default chime not applied: %v", r.Body["defaultChimeId"])
	}

	// Rename / delete rules.
	if r := a.do("PATCH", "/clips/"+clipID, mgr, map[string]string{"name": "Lunch is here"}); r.Status != 200 || r.Body["name"] != "Lunch is here" {
		t.Fatalf("rename: %d %v", r.Status, r.Body)
	}
	if r := a.do("DELETE", "/clips/"+clips.ChimeBellID, admin, nil); r.Status != 403 {
		t.Fatalf("delete builtin: %d", r.Status)
	}
	other, _ := a.login("m2@example.com", "manager")
	if r := a.do("DELETE", "/clips/"+clipID, other, nil); r.Status != 403 {
		t.Fatalf("delete someone else's: %d", r.Status)
	}
	if r := a.do("DELETE", "/clips/"+clipID, admin, nil); r.Status != 204 {
		t.Fatalf("admin delete: %d", r.Status)
	}
}
