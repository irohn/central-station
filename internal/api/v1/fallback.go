package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"central-station/internal/auth"
	"central-station/internal/fallback"
	"central-station/internal/httpx"
	"central-station/internal/zones"
)

// resolveTimeout bounds the synchronous playlist fetch done by PUT / refresh.
const resolveTimeout = 25 * time.Second

// fallbackResponse is a View plus the server-configured default (from
// FALLBACK_PLAYLIST_URL), which "Reset" restores.
type fallbackResponse struct {
	fallback.View
	EnvDefault *fallback.Settings `json:"envDefault,omitempty"`
}

func (a *api) fallbackView(v fallback.View) fallbackResponse {
	out := fallbackResponse{View: v}
	if a.Config.FallbackPlaylistURL != "" {
		out.EnvDefault = &fallback.Settings{URL: a.Config.FallbackPlaylistURL, Enabled: true, Shuffle: a.Config.FallbackShuffle}
	}
	return out
}

// GET /settings/fallback  (any signed-in user)
func (a *api) getFallback(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, a.fallbackView(rt.Fallback.Get()))
	return nil
}

// POST /settings/fallback/reset  restores the playlist configured on the server.
func (a *api) resetFallback(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	if a.Config.FallbackPlaylistURL == "" {
		return httpx.Conflict("no_default_configured", "no default playlist is configured on the server (FALLBACK_PLAYLIST_URL)")
	}
	before := rt.Fallback.Get()
	ctx, cancel := context.WithTimeout(r.Context(), resolveTimeout)
	defer cancel()
	view, err := rt.Fallback.Update(ctx, fallback.Settings{URL: a.Config.FallbackPlaylistURL, Enabled: true, Shuffle: a.Config.FallbackShuffle})
	if err != nil {
		return fallbackError(err)
	}
	a.applyFallbackChange(r, rt, before, view)
	httpx.JSON(w, http.StatusOK, a.fallbackView(view))
	return nil
}

// PUT /settings/fallback  {"url": "...", "enabled": true, "shuffle": false}
//
// Saving takes effect immediately: when the player is idle the playlist starts,
// and when a track from a previous playlist (or a now-disabled one) is playing
// it is skipped.
func (a *api) putFallback(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	var body fallback.Settings
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	before := rt.Fallback.Get()
	ctx, cancel := context.WithTimeout(r.Context(), resolveTimeout)
	defer cancel()
	view, err := rt.Fallback.Update(ctx, body)
	if err != nil {
		return fallbackError(err)
	}
	a.applyFallbackChange(r, rt, before, view)
	httpx.JSON(w, http.StatusOK, a.fallbackView(view))
	return nil
}

// POST /settings/fallback/refresh
func (a *api) refreshFallback(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), resolveTimeout)
	defer cancel()
	view, err := rt.Fallback.Refresh(ctx)
	if err != nil {
		return fallbackError(err)
	}
	if err := rt.Queue.Nudge(r.Context()); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, a.fallbackView(view))
	return nil
}

// applyFallbackChange reconciles playback with new settings.
func (a *api) applyFallbackChange(r *http.Request, rt *zones.Runtime, before, after fallback.View) {
	cur := rt.Queue.Current()
	playlistChanged := (before.Playlist == nil) != (after.Playlist == nil) ||
		(before.Playlist != nil && after.Playlist != nil && before.Playlist.ID != after.Playlist.ID)
	if cur.IsFallback() && (!after.Enabled || playlistChanged) {
		if _, err := rt.Queue.Skip(r.Context(), auth.MustUser(r).ID); err != nil {
			return
		}
	}
	if after.Enabled {
		_ = rt.Queue.Nudge(r.Context())
	}
}

func fallbackError(err error) error {
	var v *fallback.ValidationError
	if errors.As(err, &v) {
		return httpx.BadRequest("invalid_input", "%s", v.Msg)
	}
	return providerError(err)
}
