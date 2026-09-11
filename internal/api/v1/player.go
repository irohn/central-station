package v1

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"central-station/internal/httpx"
)

// GET /player  (any signed-in user)
func (a *api) getPlayer(w http.ResponseWriter, _ *http.Request) error {
	httpx.JSON(w, http.StatusOK, a.Hub.Status())
	return nil
}

// GET /player/ws  (player container; Bearer PLAYER_TOKEN)
func (a *api) playerWS(w http.ResponseWriter, r *http.Request) error {
	if a.Config.PlayerToken == "" {
		return httpx.NewError(http.StatusServiceUnavailable, "player_disabled", "PLAYER_TOKEN is not configured")
	}
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return httpx.Unauthorized("player token required")
	}
	got := strings.TrimSpace(h[7:])
	if subtle.ConstantTimeCompare([]byte(got), []byte(a.Config.PlayerToken)) != 1 {
		return httpx.Unauthorized("invalid player token")
	}
	a.Hub.ServePlayer(w, r)
	return nil
}

// GET /events/ws  (browser; session cookie or Bearer token)
func (a *api) eventsWS(w http.ResponseWriter, r *http.Request) error {
	a.Hub.ServeSpectator(w, r)
	return nil
}

// GET /auth/methods  (public) tells the login page which sign-in methods exist.
func (a *api) authMethods(w http.ResponseWriter, _ *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]bool{"google": a.Google != nil, "dev": a.Config.AuthDevMode})
	return nil
}
