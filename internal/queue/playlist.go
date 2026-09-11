package queue

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/google/uuid"

	"central-station/internal/provider"
)

// SourcePlaylist marks items that came from a user-queued playlist.
const SourcePlaylist = "playlist"

// ErrPlaylistNotFound is returned for unknown playlist ids.
var ErrPlaylistNotFound = errors.New("queued playlist not found")

// QueuedPlaylist is a whole playlist a user queued. Its tracks play one by one
// once the song queue is empty, before the default playlist. Played-through
// playlists are dropped automatically.
type QueuedPlaylist struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	SourceID string            `json:"sourceId"`
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Tracks   []*provider.Track `json:"tracks"`
	// Order is the play order over Tracks (a permutation when shuffled);
	// Order[Cursor] is the next track to play.
	Order       []int     `json:"order"`
	Cursor      int       `json:"cursor"`
	Shuffle     bool      `json:"shuffle"`
	AddedByID   string    `json:"addedById"`
	AddedByName string    `json:"addedByName"`
	AddedAt     time.Time `json:"addedAt"`
}

func (p *QueuedPlaylist) exhausted() bool { return p.Cursor >= len(p.Order) }

func (p *QueuedPlaylist) next() *provider.Track {
	if p.exhausted() {
		return nil
	}
	return p.Tracks[p.Order[p.Cursor]]
}

// PlaylistSummary is the per-playlist view embedded in State (no track list).
type PlaylistSummary struct {
	ID           string          `json:"id"`
	Provider     string          `json:"provider"`
	URL          string          `json:"url"`
	Title        string          `json:"title"`
	ThumbnailURL string          `json:"thumbnailUrl,omitempty"`
	TrackCount   int             `json:"trackCount"`
	Played       int             `json:"played"`
	Shuffle      bool            `json:"shuffle"`
	AddedByID    string          `json:"addedById"`
	AddedByName  string          `json:"addedByName"`
	AddedAt      time.Time       `json:"addedAt"`
	Next         *provider.Track `json:"next,omitempty"`
	// Playing is true while the current item comes from this playlist.
	Playing bool `json:"playing"`
}

// PlaylistChange is the playlist side effect of a Transition, applied in the
// same transaction: cursors that advanced and playlists that are gone.
type PlaylistChange struct {
	Updates []*QueuedPlaylist
	Removes []string
}

func (c *PlaylistChange) empty() bool {
	return c == nil || (len(c.Updates) == 0 && len(c.Removes) == 0)
}

// AddPlaylist queues a playlist behind the ones already waiting. If nothing is
// playing it starts immediately.
func (s *Service) AddPlaylist(ctx context.Context, pl *provider.Playlist, userID, userName string, shuffle bool) (*QueuedPlaylist, error) {
	if pl == nil || len(pl.Tracks) == 0 {
		return nil, fmt.Errorf("%w: playlist has no tracks", provider.ErrNotFound)
	}
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	q := &QueuedPlaylist{
		ID: uuid.NewString(), Provider: pl.Provider, SourceID: pl.ID, URL: pl.URL, Title: pl.Title,
		Tracks: pl.Tracks, Shuffle: shuffle, AddedByID: userID, AddedByName: userName, AddedAt: s.now(),
	}
	q.Order = s.orderLocked(len(pl.Tracks), shuffle)
	if err := s.store.InsertPlaylist(ctx, q); err != nil {
		return nil, err
	}
	s.playlists = append(s.playlists, q)
	s.touch()
	if s.current == nil {
		if err := s.transitionLocked(ctx, nil); err != nil {
			return q, err
		}
	}
	return q, nil
}

// GetPlaylist returns a queued playlist (for permission checks).
func (s *Service) GetPlaylist(id string) (*QueuedPlaylist, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.playlists {
		if p.ID == id {
			cp := *p
			return &cp, nil
		}
	}
	return nil, ErrPlaylistNotFound
}

// RemovePlaylist drops a queued playlist. If its track is playing, that track
// is skipped too (recorded in history as skipped by byUserID).
func (s *Service) RemovePlaylist(ctx context.Context, id, byUserID string) (*QueuedPlaylist, error) {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	pl := s.findPlaylistLocked(id)
	if pl == nil {
		return nil, ErrPlaylistNotFound
	}
	if s.current != nil && s.current.PlaylistID == id {
		ended := &HistoryEntry{Item: s.current, StartedAt: s.startedAt, EndedAt: s.now(), EndReason: EndSkipped, EndedByID: byUserID}
		if err := s.advanceLocked(ctx, ended, id); err != nil {
			return nil, err
		}
		return pl, nil
	}
	if err := s.store.DeletePlaylist(ctx, id); err != nil {
		return nil, err
	}
	s.dropPlaylistLocked(id)
	s.touch()
	return pl, nil
}

// SetPlaylistShuffle turns shuffle on or off for the tracks not yet played.
func (s *Service) SetPlaylistShuffle(ctx context.Context, id string, shuffle bool) (*QueuedPlaylist, error) {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	pl := s.findPlaylistLocked(id)
	if pl == nil {
		return nil, ErrPlaylistNotFound
	}
	if pl.Shuffle == shuffle {
		return pl, nil
	}
	cp := *pl
	cp.Shuffle = shuffle
	played := append([]int(nil), pl.Order[:pl.Cursor]...)
	rest := append([]int(nil), pl.Order[pl.Cursor:]...)
	if shuffle {
		s.rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	} else {
		sort.Ints(rest)
	}
	cp.Order = append(played, rest...)
	if err := s.store.UpdatePlaylist(ctx, &cp); err != nil {
		return nil, err
	}
	*pl = cp
	s.touch()
	return pl, nil
}

func (s *Service) findPlaylistLocked(id string) *QueuedPlaylist {
	for _, p := range s.playlists {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (s *Service) dropPlaylistLocked(id string) {
	for i, p := range s.playlists {
		if p.ID == id {
			s.playlists = append(s.playlists[:i], s.playlists[i+1:]...)
			return
		}
	}
}

func (s *Service) orderLocked(n int, shuffle bool) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	if shuffle {
		s.rng.Shuffle(n, func(i, j int) { order[i], order[j] = order[j], order[i] })
	}
	return order
}

func (s *Service) playlistSummariesLocked() []PlaylistSummary {
	out := make([]PlaylistSummary, 0, len(s.playlists))
	for _, p := range s.playlists {
		sm := PlaylistSummary{
			ID: p.ID, Provider: p.Provider, URL: p.URL, Title: p.Title, TrackCount: len(p.Tracks), Played: p.Cursor,
			Shuffle: p.Shuffle, AddedByID: p.AddedByID, AddedByName: p.AddedByName, AddedAt: p.AddedAt, Next: p.next(),
			Playing: s.current != nil && s.current.PlaylistID == p.ID,
		}
		if len(p.Tracks) > 0 {
			sm.ThumbnailURL = p.Tracks[0].ThumbnailURL
		}
		out = append(out, sm)
	}
	return out
}

func newRNG() *rand.Rand { return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7)) }
