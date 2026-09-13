package v1

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/config"
	"central-station/internal/provider"
	"central-station/internal/settings"
	"central-station/internal/store"
	"central-station/internal/tts"
)

// fakeTTS serves two voices and a 1-second silent WAV.
func fakeTTS(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var voicesUsed []string
	mux := http.NewServeMux()
	mux.HandleFunc("/voices", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"default": "v-female", "voices": []map[string]string{
			{"id": "v-female", "label": "Female", "language": "English"}, {"id": "v-male", "label": "Male", "language": "English"}}})
	})
	mux.HandleFunc("/synthesize", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		voicesUsed = append(voicesUsed, req["voice"])
		var b bytes.Buffer // 16-bit mono 8 kHz, 1 s
		b.WriteString("RIFF")
		_ = binary.Write(&b, binary.LittleEndian, uint32(36+16000))
		b.WriteString("WAVEfmt ")
		for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(8000), uint32(16000), uint16(2), uint16(16)} {
			_ = binary.Write(&b, binary.LittleEndian, v)
		}
		b.WriteString("data")
		_ = binary.Write(&b, binary.LittleEndian, uint32(16000))
		b.Write(make([]byte, 16000))
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(b.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &voicesUsed
}

func newTTSAPI(t *testing.T, extra map[string]string) (*testAPI, *[]string) {
	t.Helper()
	ttsSrv, used := fakeTTS(t)
	env := map[string]string{"AUTH_DEV_MODE": "true", "PLAYER_TOKEN": "test-player-token-123", "TTS_URL": ttsSrv.URL}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := provider.NewRegistry("youtube", fakeProvider{})
	cs := clips.NewMemory()
	_ = clips.EnsureBuiltins(context.Background(), cs)
	srv := httptest.NewServer(Handler(Deps{Config: cfg, Users: store.NewMemory(), Zones: testZones(t, cfg, reg), Providers: reg,
		Clips: cs, TTS: tts.New(cfg.TTSURL), Settings: settings.NewMemory(), Sessions: auth.NewSessions(cfg.SessionSecret, time.Hour, false)}))
	t.Cleanup(srv.Close)
	return &testAPI{t: t, srv: srv}, used
}

func TestTTSVoicesAndPreview(t *testing.T) {
	a, used := newTTSAPI(t, map[string]string{"TTS_MAX_CHARS": "20"})
	mgr, _ := a.login("m@example.com", "manager")
	admin, _ := a.login("root@example.com", "admin")

	r := a.do("GET", "/clips", mgr, nil)
	if r.Body["ttsAvailable"] != true || r.Body["defaultVoice"] != "v-female" || len(r.Body["voices"].([]any)) != 2 || r.Body["maxTextChars"].(float64) != 20 {
		t.Fatalf("list: %v", r.Body)
	}
	r = a.do("POST", "/clips/tts", mgr, map[string]string{"text": "hello there"})
	if r.Status != 201 || r.Body["voice"] != "v-female" || r.Body["durationSeconds"].(float64) != 1 || r.Body["name"] != "hello there" {
		t.Fatalf("default voice: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "hi", "voice": "v-male", "name": "Hi"}); r.Status != 201 || r.Body["voice"] != "v-male" {
		t.Fatalf("explicit voice: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "hi", "voice": "nope"}); r.Status != 400 || errCode(r) != "unknown_voice" {
		t.Fatalf("unknown voice: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "this text is far too long for the cap"}); r.Status != 400 || errCode(r) != "too_long" {
		t.Fatalf("too long: %d %v", r.Status, r.Body)
	}
	if got := *used; len(got) != 2 || got[0] != "v-female" || got[1] != "v-male" {
		t.Fatalf("voices sent to the service: %v", got)
	}

	req, _ := http.NewRequest("POST", a.srv.URL+"/clips/tts/preview", bytes.NewBufferString(`{"text":"preview","voice":"v-male"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mgr)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("preview: %v %v", err, res)
	}
	res.Body.Close()
	if r := a.do("GET", "/clips", mgr, nil); r.Body["savedCount"].(float64) != 2 {
		t.Fatalf("preview must not save: %v", r.Body["savedCount"])
	}

	if r := a.do("PUT", "/settings/tts", mgr, map[string]string{"voice": "v-male"}); r.Status != 403 {
		t.Fatalf("manager set voice: %d", r.Status)
	}
	if r := a.do("PUT", "/settings/tts", admin, map[string]string{"voice": "nope"}); r.Status != 400 {
		t.Fatalf("unknown default: %d", r.Status)
	}
	if r := a.do("PUT", "/settings/tts", admin, map[string]string{"voice": "v-male"}); r.Status != 200 {
		t.Fatalf("set default voice: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/clips", mgr, nil); r.Body["defaultVoice"] != "v-male" {
		t.Fatalf("default voice not applied: %v", r.Body["defaultVoice"])
	}
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "again"}); r.Body["voice"] != "v-male" {
		t.Fatalf("new default not used: %v", r.Body)
	}
}

func TestClipCap(t *testing.T) {
	a, _ := newTTSAPI(t, map[string]string{"CLIPS_MAX_SAVED": "2"})
	mgr, _ := a.login("m@example.com", "manager")
	for i := 0; i < 2; i++ {
		if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "x"}); r.Status != 201 {
			t.Fatalf("clip %d: %d %v", i, r.Status, r.Body)
		}
	}
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "one too many"}); r.Status != 409 || errCode(r) != "clip_limit" {
		t.Fatalf("cap: %d %v", r.Status, r.Body)
	}
	if r := a.upload(mgr, "rec", "recording", "audio/mp4", []byte("abc"), "1"); r.Status != 409 {
		t.Fatalf("upload past cap: %d", r.Status)
	}
	list := a.do("GET", "/clips", mgr, nil)
	if list.Body["savedCount"].(float64) != 2 || list.Body["maxSaved"].(float64) != 2 || len(list.Body["clips"].([]any)) != 5 {
		t.Fatalf("counts: %v", list.Body)
	}
	var victim string
	for _, c := range list.Body["clips"].([]any) {
		if m := c.(map[string]any); m["builtin"] == false {
			victim = m["id"].(string)
		}
	}
	if r := a.do("DELETE", "/clips/"+victim, mgr, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if r := a.do("POST", "/clips/tts", mgr, map[string]string{"text": "fits again"}); r.Status != 201 {
		t.Fatalf("after delete: %d %v", r.Status, r.Body)
	}
}
