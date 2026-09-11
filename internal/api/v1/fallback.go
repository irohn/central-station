package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	"central-station/internal/auth"
	"central-station/internal/fallback"
	"central-station/internal/httpx"
)

// resolveTimeout bounds the synchronous playlist fetch done by PUT / refresh.
const resolveTimeout = 25 * time.Second

func (a *api) requireFallback() error {
	if a.Fallback == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "fallback_unavailable", "the default playlist is not available on this server")
	}
	return nil
}

// GET /settings/fallback  (any signed-in user)
func (a *api) getFallback(w http.ResponseWriter, _ *http.Request) error {
	if err := a.requireFallback(); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, a.Fallback.Get())
	return nil
}

// PUT /settings/fallback  {"url": "...", "enabled": true, "shuffle": false}
//
// Saving takes effect immediately: when the player is idle the playlist starts,
// and when a track from a previous playlist (or a now-disabled one) is playing
// it is skipped.
func (a *api) putFallback(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireFallback(); err != nil {
		return err
	}
	var body fallback.Settings
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	before := a.Fallback.Get()
	ctx, cancel := context.WithTimeout(r.Context(), resolveTimeout)
	defer cancel()
	view, err := a.Fallback.Update(ctx, body)
	if err != nil {
		return fallbackError(err)
	}
	a.applyFallbackChange(r, before, view)
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// POST /settings/fallback/refresh
func (a *api) refreshFallback(w http.ResponseWriter, r *http.Request) error {
	if err := a.requireFallback(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), resolveTimeout)
	defer cancel()
	view, err := a.Fallback.Refresh(ctx)
	if err != nil {
		return fallbackError(err)
	}
	if err := a.Queue.Nudge(r.Context()); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// applyFallbackChange reconciles playback with new settings.
func (a *api) applyFallbackChange(r *http.Request, before, after fallback.View) {
	cur := a.Queue.Current()
	playlistChanged := (before.Playlist == nil) != (after.Playlist == nil) ||
		(before.Playlist != nil && after.Playlist != nil && before.Playlist.ID != after.Playlist.ID)
	if cur.IsFallback() && (!after.Enabled || playlistChanged) {
		if _, err := a.Queue.Skip(r.Context(), auth.MustUser(r).ID); err != nil {
			return
		}
	}
	if after.Enabled {
		_ = a.Queue.Nudge(r.Context())
	}
}

func fallbackError(err error) error {
	var v *fallback.ValidationError
	if errors.As(err, &v) {
		return httpx.BadRequest("invalid_input", "%s", v.Msg)
	}
	return providerError(err)
}
