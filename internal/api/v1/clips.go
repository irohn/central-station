package v1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/httpx"
	"central-station/internal/player"
	"central-station/internal/rbac"
	"central-station/internal/tts"
)

const (
	chimeKey = "chime.default"
	voiceKey = "tts.voice"
)

// userClipCount counts clips that count against CLIPS_MAX_SAVED (bundled chimes don't).
func (a *api) userClipCount(ctx context.Context) (int, error) {
	list, err := a.Clips.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if !c.Builtin {
			n++
		}
	}
	return n, nil
}

func (a *api) checkClipCap(ctx context.Context) error {
	n, err := a.userClipCount(ctx)
	if err != nil {
		return err
	}
	if n >= a.Config.ClipsMaxSaved {
		return httpx.Conflict("clip_limit", "the limit of %d saved clips is reached; delete one first", a.Config.ClipsMaxSaved)
	}
	return nil
}

// voices returns the TTS voices and the effective default (admin setting, else service default).
func (a *api) voices(ctx context.Context) ([]tts.Voice, string) {
	if !a.TTS.Enabled() {
		return nil, ""
	}
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, def, err := a.TTS.Voices(vctx)
	if err != nil {
		return nil, ""
	}
	if a.Settings != nil {
		if raw, err := a.Settings.Get(ctx, voiceKey); err == nil {
			want := strings.Trim(string(raw), "\"")
			for _, v := range list {
				if v.ID == want {
					def = want
					break
				}
			}
		}
	}
	return list, def
}

func (a *api) requireClips() error {
	if a.Clips == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "clips_unavailable", "clips are not available on this server")
	}
	return nil
}

// defaultChimeID returns the configured default chime (falls back to the bundled one).
func (a *api) defaultChimeID(ctx context.Context) string {
	if a.Settings != nil {
		if raw, err := a.Settings.Get(ctx, chimeKey); err == nil {
			id := strings.Trim(string(raw), "\"")
			if id != "" {
				return id
			}
		}
	}
	return clips.DefaultChimeID
}

// GET /clips
func (a *api) listClips(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	list, err := a.Clips.List(r.Context())
	if err != nil {
		return err
	}
	if list == nil {
		list = []*clips.Clip{}
	}
	saved := 0
	for _, c := range list {
		if !c.Builtin {
			saved++
		}
	}
	voices, defVoice := a.voices(r.Context())
	if voices == nil {
		voices = []tts.Voice{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"clips":          list,
		"defaultChimeId": a.defaultChimeID(r.Context()),
		"ttsAvailable":   a.TTS.Enabled(),
		"voices":         voices,
		"defaultVoice":   defVoice,
		"savedCount":     saved,
		"maxSaved":       a.Config.ClipsMaxSaved,
		"maxDurationSec": a.Config.ClipsMaxDurationSeconds,
		"maxBytes":       a.Config.ClipsMaxBytes,
		"maxLoops":       player.MaxBroadcastLoops,
		"maxTextChars":   a.Config.TTSMaxChars,
	})
	return nil
}

// POST /clips  multipart: file, name, kind (recording|chime), durationSeconds
func (a *api) uploadClip(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	u := auth.MustUser(r)
	if err := a.checkClipCap(r.Context()); err != nil {
		return err
	}
	maxBytes := int64(a.Config.ClipsMaxBytes)
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+64<<10)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		return httpx.BadRequest("invalid_upload", "could not read upload (max %d MB): %v", maxBytes>>20, err)
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("invalid_upload", "file is required")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return err
	}
	if len(data) == 0 || int64(len(data)) > maxBytes {
		return httpx.BadRequest("invalid_upload", "file must be between 1 byte and %d MB", maxBytes>>20)
	}
	mime := clips.NormalizeMime(hdr.Header.Get("Content-Type"))
	if mime == "" || mime == "application/octet-stream" {
		mime = clips.NormalizeMime(r.FormValue("mime"))
	}
	if !clips.AllowedMimes[mime] {
		return httpx.BadRequest("unsupported_audio", "unsupported audio type %q", mime)
	}
	kind := clips.Kind(strings.ToLower(strings.TrimSpace(r.FormValue("kind"))))
	if kind == "" {
		kind = clips.KindRecording
	}
	if kind != clips.KindRecording && kind != clips.KindChime {
		return httpx.BadRequest("invalid_kind", "kind must be recording or chime")
	}
	dur, _ := strconv.ParseFloat(r.FormValue("durationSeconds"), 64)
	if dur < 0 || dur > float64(a.Config.ClipsMaxDurationSeconds)+1 {
		return httpx.BadRequest("too_long", "recordings are limited to %d seconds", a.Config.ClipsMaxDurationSeconds)
	}
	if mime == "audio/wav" && dur == 0 {
		dur = tts.WAVDuration(data)
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = fmt.Sprintf("%s %s", strings.Title(string(kind)), time.Now().Format("Jan 2 15:04"))
	}
	c := &clips.Clip{ID: uuid.NewString(), Name: name, Kind: kind, Mime: mime, Size: len(data), DurationSeconds: dur,
		CreatedByID: u.ID, CreatedByName: u.Name, CreatedAt: time.Now()}
	if err := a.Clips.Insert(r.Context(), c, data); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, c)
	return nil
}

type ttsRequest struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

// synthesize validates a TTS request and returns the audio and the voice used.
func (a *api) synthesize(r *http.Request) (*ttsRequest, []byte, error) {
	if !a.TTS.Enabled() {
		return nil, nil, httpx.NewError(http.StatusServiceUnavailable, "tts_unavailable", "text-to-speech is not configured on this server")
	}
	var body ttsRequest
	if err := httpx.Decode(r, &body, false); err != nil {
		return nil, nil, err
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		return nil, nil, httpx.BadRequest("invalid_input", "text is required")
	}
	if len([]rune(body.Text)) > a.Config.TTSMaxChars {
		return nil, nil, httpx.BadRequest("too_long", "text is limited to %d characters", a.Config.TTSMaxChars)
	}
	voices, def := a.voices(r.Context())
	body.Voice = strings.TrimSpace(body.Voice)
	if body.Voice == "" {
		body.Voice = def
	} else {
		ok := false
		for _, v := range voices {
			if v.ID == body.Voice {
				ok = true
				break
			}
		}
		if !ok {
			return nil, nil, httpx.BadRequest("unknown_voice", "unknown voice %q", body.Voice)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	data, err := a.TTS.Synthesize(ctx, body.Text, body.Voice)
	if err != nil {
		return nil, nil, httpx.BadGateway("tts_failed", "%s", err.Error())
	}
	return &body, data, nil
}

// POST /clips/tts  {"name": "...", "text": "...", "voice": "..."}  saves a clip
func (a *api) ttsClip(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	if err := a.checkClipCap(r.Context()); err != nil {
		return err
	}
	u := auth.MustUser(r)
	body, data, err := a.synthesize(r)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = body.Text
		if n := []rune(name); len(n) > 40 {
			name = string(n[:40]) + "…"
		}
	}
	c := &clips.Clip{ID: uuid.NewString(), Name: name, Kind: clips.KindTTS, Mime: "audio/wav", Size: len(data), DurationSeconds: tts.WAVDuration(data),
		Text: body.Text, Voice: body.Voice, CreatedByID: u.ID, CreatedByName: u.Name, CreatedAt: time.Now()}
	if err := a.Clips.Insert(r.Context(), c, data); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, c)
	return nil
}

// POST /clips/tts/preview  {"text": "...", "voice": "..."}  returns audio without saving
func (a *api) ttsPreview(w http.ResponseWriter, r *http.Request) error {
	_, data, err := a.synthesize(r)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}

// PUT /settings/tts  {"voice": "..."}  (admins) — the voice used when a request does not pick one.
func (a *api) setDefaultVoice(w http.ResponseWriter, r *http.Request) error {
	if a.Settings == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "settings_unavailable", "settings are not available")
	}
	var body struct {
		Voice string `json:"voice"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	voices, _ := a.voices(r.Context())
	ok := false
	for _, v := range voices {
		if v.ID == body.Voice {
			ok = true
			break
		}
	}
	if !ok {
		return httpx.BadRequest("unknown_voice", "unknown voice %q", body.Voice)
	}
	if err := a.Settings.Set(r.Context(), voiceKey, []byte(strconv.Quote(body.Voice))); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"defaultVoice": body.Voice})
	return nil
}

// GET /clips/{id}/audio  (browser preview)
func (a *api) clipAudio(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	return a.serveClipAudio(w, r)
}

func (a *api) serveClipAudio(w http.ResponseWriter, r *http.Request) error {
	c, data, err := a.Clips.Audio(r.Context(), r.PathValue("id"))
	if errors.Is(err, clips.ErrNotFound) {
		return httpx.NotFound("clip_not_found", "clip %s not found", r.PathValue("id"))
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", c.Mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return nil
}

// PATCH /clips/{id}  {"name": "..."}
func (a *api) renameClip(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		return httpx.BadRequest("invalid_input", "name is required")
	}
	c, err := a.Clips.Rename(r.Context(), r.PathValue("id"), body.Name)
	if errors.Is(err, clips.ErrNotFound) {
		return httpx.NotFound("clip_not_found", "clip not found")
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, c)
	return nil
}

// DELETE /clips/{id}  (creator, or settings:manage; bundled chimes never)
func (a *api) deleteClip(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	u := auth.MustUser(r)
	c, err := a.Clips.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, clips.ErrNotFound) {
		return httpx.NotFound("clip_not_found", "clip not found")
	}
	if err != nil {
		return err
	}
	if c.Builtin {
		return httpx.Forbidden("bundled chimes cannot be deleted")
	}
	if c.CreatedByID != u.ID && !u.Role.Has(rbac.SettingsManage) {
		return httpx.Forbidden("you can only delete clips you created")
	}
	if err := a.Clips.Delete(r.Context(), c.ID); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// PUT /settings/chime  {"clipId": "..."}  (admins) — the chime used when a
// broadcast does not choose one.
func (a *api) setDefaultChime(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	if a.Settings == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "settings_unavailable", "settings are not available")
	}
	var body struct {
		ClipID string `json:"clipId"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	c, err := a.Clips.Get(r.Context(), body.ClipID)
	if errors.Is(err, clips.ErrNotFound) {
		return httpx.NotFound("clip_not_found", "clip not found")
	}
	if err != nil {
		return err
	}
	if c.Kind != clips.KindChime {
		return httpx.BadRequest("not_a_chime", "clip %q is not a chime", c.Name)
	}
	if err := a.Settings.Set(r.Context(), chimeKey, []byte(strconv.Quote(c.ID))); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"defaultChimeId": c.ID})
	return nil
}

// GET /broadcasts
func (a *api) getBroadcasts(w http.ResponseWriter, _ *http.Request) error {
	cur, queued := a.Hub.Broadcasts()
	httpx.JSON(w, http.StatusOK, map[string]any{"current": cur, "queued": queued})
	return nil
}

// POST /broadcasts  {"clipId": "...", "loops": 1-10, "chimeId": "" | "none" | "<clip id>"}
func (a *api) sendBroadcast(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireClips(); err != nil {
		return err
	}
	u := auth.MustUser(r)
	var body struct {
		ClipID  string `json:"clipId"`
		Loops   int    `json:"loops"`
		ChimeID string `json:"chimeId"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Loops == 0 {
		body.Loops = 1
	}
	if body.Loops < 1 || body.Loops > player.MaxBroadcastLoops {
		return httpx.BadRequest("invalid_loops", "loops must be between 1 and %d", player.MaxBroadcastLoops)
	}
	c, err := a.Clips.Get(r.Context(), body.ClipID)
	if errors.Is(err, clips.ErrNotFound) {
		return httpx.NotFound("clip_not_found", "clip not found")
	}
	if err != nil {
		return err
	}
	b := &player.Broadcast{ID: uuid.NewString(), ClipID: c.ID, ClipName: c.Name, ClipURL: "/api/v1/player/clips/" + c.ID + "/audio", Loops: body.Loops, ByID: u.ID, ByName: u.Name}
	chimeID := strings.TrimSpace(body.ChimeID)
	if chimeID == "" {
		chimeID = a.defaultChimeID(r.Context())
	}
	if chimeID != "none" && c.Kind != clips.KindChime {
		ch, err := a.Clips.Get(r.Context(), chimeID)
		if errors.Is(err, clips.ErrNotFound) {
			return httpx.NotFound("clip_not_found", "chime not found")
		}
		if err != nil {
			return err
		}
		if ch.Kind != clips.KindChime {
			return httpx.BadRequest("not_a_chime", "clip %q is not a chime", ch.Name)
		}
		b.ChimeURL = "/api/v1/player/clips/" + ch.ID + "/audio"
	}
	pos, err := a.Hub.SendBroadcast(b)
	if errors.Is(err, player.ErrPlayerOffline) {
		return httpx.Conflict("player_offline", "no player is connected, so nothing would be heard")
	}
	if err != nil {
		return err
	}
	cur, queued := a.Hub.Broadcasts()
	httpx.JSON(w, http.StatusAccepted, map[string]any{"id": b.ID, "position": pos, "current": cur, "queued": queued})
	return nil
}
