package v1

import (
	"errors"
	"net/http"
	"strings"

	"central-station/internal/auth"
	"central-station/internal/httpx"
	"central-station/internal/player"
	"central-station/internal/zones"
)

// POST /playback/volume  {"volume": 60}   (managers)
func (a *api) setVolume(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	var body struct {
		Volume *int `json:"volume"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Volume == nil {
		return httpx.BadRequest("invalid_input", "volume is required")
	}
	if err := rt.Hub.SetVolume(r.Context(), *body.Volume); err != nil {
		if errors.Is(err, player.ErrBadVolume) {
			return httpx.BadRequest("invalid_volume", "%s", err.Error())
		}
		return err
	}
	httpx.JSON(w, http.StatusOK, rt.Hub.Status())
	return nil
}

// POST /playback/output  {"device": "pulse/alsa_output.usb-…" | "auto"}   (managers)
//
// Picks which of the player's outputs the zone plays through. Valid ids are
// "auto" (the node's default output) and the ids listed in the player status
// (audioDevices); while no player is connected any id is accepted and the
// player validates it when it connects.
func (a *api) setOutput(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	var body struct {
		Device *string `json:"device"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Device == nil {
		return httpx.BadRequest("invalid_input", "device is required (\"auto\" or an id from audioDevices)")
	}
	if err := rt.Hub.SetAudioDevice(r.Context(), strings.TrimSpace(*body.Device)); err != nil {
		if errors.Is(err, player.ErrUnknownDevice) {
			return httpx.BadRequest("unknown_device", "%s", err.Error()).WithDetails(map[string]any{"audioDevices": rt.Hub.Status().AudioDevices})
		}
		return err
	}
	httpx.JSON(w, http.StatusOK, rt.Hub.Status())
	return nil
}

// GET /player  (any signed-in user)
func (a *api) getPlayer(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, rt.Hub.Status())
	return nil
}

// playerAuth authenticates a player container by its zone token (or the
// legacy PLAYER_TOKEN) and returns the zone it belongs to.
func (a *api) playerAuth(r *http.Request) (*zones.Runtime, error) {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return nil, httpx.Unauthorized("player token required")
	}
	rt, err := a.Zones.Authenticate(r.Context(), strings.TrimSpace(h[7:]))
	if errors.Is(err, zones.ErrBadToken) {
		return nil, httpx.Unauthorized("invalid player token")
	}
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// GET /player/ws  (player container; Bearer <zone token>)
func (a *api) playerWS(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.playerAuth(r)
	if err != nil {
		return err
	}
	rt.Hub.ServePlayer(w, r)
	return nil
}

// GET /player/clips/{id}/audio  (player container fetches announcement audio)
func (a *api) playerClipAudio(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.playerAuth(r); err != nil {
		return err
	}
	return a.serveClipAudio(w, r)
}

// GET /events/ws  (browser; session cookie or Bearer token)
func (a *api) eventsWS(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	rt.Hub.ServeSpectator(w, r)
	return nil
}

// GET /auth/methods  (public) tells the login page which sign-in methods exist.
func (a *api) authMethods(w http.ResponseWriter, _ *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"google":               a.Google != nil,
		"dev":                  a.Config.AuthDevMode,
		"password":             a.Config.AuthPasswordEnabled,
		"passwordVerification": a.verificationMode(),
		"passwordMinLength":    auth.PasswordMinLength,
	})
	return nil
}
