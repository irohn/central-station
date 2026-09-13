package v1

import (
	"errors"
	"net/http"
	"strconv"

	"central-station/internal/auth"
	"central-station/internal/httpx"
	"central-station/internal/provider"
	"central-station/internal/queue"
	"central-station/internal/rbac"
	"central-station/internal/zones"
)

// resolveRequest is the body of POST /tracks/resolve and POST /queue.
//
//	{"input": "https://www.youtube.com/watch?v=6TWJaFD6R2s"}
//	{"input": "daft punk around the world", "provider": "youtube"}
//
// "provider" only applies to search strings; for URLs the provider is
// detected from the host.
type resolveRequest struct {
	Input    string `json:"input"`
	Provider string `json:"provider,omitempty"`
}

func (a *api) resolve(r *http.Request) (*provider.Resolution, error) {
	var body resolveRequest
	if err := httpx.Decode(r, &body, false); err != nil {
		return nil, err
	}
	res, err := a.Providers.Resolve(r.Context(), body.Input, body.Provider)
	if err != nil {
		return nil, providerError(err)
	}
	return res, nil
}

// GET /providers
func (a *api) listProviders(w http.ResponseWriter, _ *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{"providers": a.Providers.List(), "default": a.Providers.DefaultID()})
	return nil
}

// POST /tracks/resolve
func (a *api) resolveTrack(w http.ResponseWriter, r *http.Request) error {
	res, err := a.resolve(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// GET /queue
func (a *api) getQueue(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// POST /queue
//
// A playlist URL (youtube.com/playlist?list=..., music.youtube.com/...)
// queues the whole playlist (permission playlist:write) and answers with
// "kind":"playlist"; anything else queues a single track ("kind":"track").
// Optional "shuffle" (default true) applies to playlists.
func (a *api) addToQueue(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	u := auth.MustUser(r)
	var body struct {
		resolveRequest
		Shuffle *bool `json:"shuffle,omitempty"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	res, err := a.Providers.Resolve(r.Context(), body.Input, body.Provider)
	if errors.Is(err, provider.ErrInvalidURL) {
		// Not a playable video URL: maybe a playlist.
		pl, perr := a.Providers.ResolvePlaylist(r.Context(), body.Input)
		if perr == nil {
			if !u.Role.Has(rbac.PlaylistWrite) {
				return httpx.Forbidden("role %q lacks permission %q", u.Role, rbac.PlaylistWrite)
			}
			shuffle := body.Shuffle == nil || *body.Shuffle
			qp, err := rt.Queue.AddPlaylist(r.Context(), pl, u.ID, u.Name, shuffle)
			if err != nil {
				return providerError(err)
			}
			httpx.JSON(w, http.StatusCreated, map[string]any{
				"kind":     "playlist",
				"playlist": playlistSummary(rt.Queue.Snapshot(), qp.ID),
				"query":    pl.URL,
				"queue":    rt.Queue.Snapshot(),
			})
			return nil
		}
		if !errors.Is(perr, provider.ErrNotPlaylist) {
			err = perr
		}
	}
	if err != nil {
		return providerError(err)
	}
	item, err := rt.Queue.Add(r.Context(), res.Track, u.ID, u.Name)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"kind":      "track",
		"item":      item,
		"inputKind": res.Kind,
		"query":     res.Query,
		"queue":     rt.Queue.Snapshot(),
	})
	return nil
}

func playlistSummary(s queue.State, id string) *queue.PlaylistSummary {
	for i := range s.Playlists {
		if s.Playlists[i].ID == id {
			return &s.Playlists[i]
		}
	}
	return nil
}

// playlistAccess allows owners (playlist:write) and managers (playlist:remove_any).
func (a *api) playlistAccess(r *http.Request, rt *zones.Runtime, id string) (*queue.QueuedPlaylist, error) {
	u := auth.MustUser(r)
	pl, err := rt.Queue.GetPlaylist(id)
	if err != nil {
		return nil, queueError(err)
	}
	switch {
	case u.Role.Has(rbac.PlaylistRemoveAny):
	case u.Role.Has(rbac.PlaylistWrite) && pl.AddedByID == u.ID:
	case u.Role.Has(rbac.PlaylistWrite):
		return nil, httpx.Forbidden("you can only change playlists you added")
	default:
		return nil, httpx.Forbidden("role %q lacks permission %q", u.Role, rbac.PlaylistWrite)
	}
	return pl, nil
}

// DELETE /queue/playlists/{id}  (skip the whole playlist; owners or managers)
func (a *api) removePlaylist(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	if _, err := a.playlistAccess(r, rt, id); err != nil {
		return err
	}
	if _, err := rt.Queue.RemovePlaylist(r.Context(), id, auth.MustUser(r).ID); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// PATCH /queue/playlists/{id}  {"shuffle": false}  (owners or managers)
func (a *api) updatePlaylist(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	var body struct {
		Shuffle *bool `json:"shuffle"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Shuffle == nil {
		return httpx.BadRequest("invalid_input", "shuffle is required")
	}
	if _, err := a.playlistAccess(r, rt, id); err != nil {
		return err
	}
	if _, err := rt.Queue.SetPlaylistShuffle(r.Context(), id, *body.Shuffle); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// DELETE /queue/{id}
// Managers may remove anything; collaborators only their own items.
func (a *api) removeFromQueue(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	u := auth.MustUser(r)
	id := r.PathValue("id")
	if cur := rt.Queue.Current(); cur != nil && cur.ID == id {
		return httpx.Conflict("item_is_playing", "item %s is currently playing; use POST /playback/skip", id)
	}
	item, err := rt.Queue.Get(id)
	if err != nil {
		return queueError(err)
	}
	switch {
	case u.Role.Has(rbac.QueueRemoveAny):
	case u.Role.Has(rbac.QueueRemoveOwn) && item.AddedByID == u.ID:
	case u.Role.Has(rbac.QueueRemoveOwn):
		return httpx.Forbidden("you can only remove songs you added")
	default:
		return httpx.Forbidden("role %q lacks permission %q", u.Role, rbac.QueueRemoveOwn)
	}
	if _, err := rt.Queue.Remove(r.Context(), id); err != nil {
		return queueError(err)
	}
	httpx.NoContent(w)
	return nil
}

// PATCH /queue/{id}  {"position": 0}
func (a *api) moveInQueue(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	var body struct {
		Position *int `json:"position"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if body.Position == nil {
		return httpx.BadRequest("invalid_input", "position is required")
	}
	if err := rt.Queue.Move(r.Context(), r.PathValue("id"), *body.Position); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// GET /history?limit=50
func (a *api) getHistory(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 500 {
			return httpx.BadRequest("invalid_limit", "limit must be an integer between 1 and 500")
		}
		limit = n
	}
	entries, err := rt.Queue.History(r.Context(), limit)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []*queue.HistoryEntry{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries})
	return nil
}

// GET /playback
func (a *api) getPlayback(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	s := rt.Queue.Snapshot()
	httpx.JSON(w, http.StatusOK, map[string]any{
		"status": s.Status, "current": s.Current, "startedAt": s.StartedAt, "queued": len(s.Items), "updatedAt": s.UpdatedAt,
		"player": rt.Hub.Status(), "zoneId": rt.Zone.ID,
	})
	return nil
}

// POST /playback/skip
func (a *api) skip(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	if _, err := rt.Queue.Skip(r.Context(), auth.MustUser(r).ID); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// POST /playback/complete  (reported by a player integration when a track ends)
func (a *api) complete(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	if _, err := rt.Queue.Complete(r.Context(), auth.MustUser(r).ID); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// POST /playback/pause
func (a *api) pause(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	if err := rt.Queue.Pause(r.Context()); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

// POST /playback/resume
func (a *api) resume(w http.ResponseWriter, r *http.Request) error {
	rt, err := a.rt(r)
	if err != nil {
		return err
	}
	if err := rt.Queue.Resume(r.Context()); err != nil {
		return queueError(err)
	}
	httpx.JSON(w, http.StatusOK, rt.Queue.Snapshot())
	return nil
}

func queueError(err error) error {
	switch {
	case errors.Is(err, queue.ErrItemNotFound):
		return httpx.NotFound("item_not_found", "%s", err.Error())
	case errors.Is(err, queue.ErrPlaylistNotFound):
		return httpx.NotFound("playlist_not_found", "%s", err.Error())
	case errors.Is(err, queue.ErrNotPlaying):
		return httpx.Conflict("not_playing", "%s", err.Error())
	case errors.Is(err, queue.ErrBadPosition):
		return httpx.BadRequest("invalid_position", "%s", err.Error())
	}
	return err
}
