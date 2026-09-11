// Package fallback implements the office "default playlist": a provider
// playlist that plays whenever the user queue is empty. Managers configure it
// from the UI; the state (settings, the expanded playlist and the play cursor)
// is persisted in the settings store.
package fallback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"central-station/internal/provider"
	"central-station/internal/queue"
	"central-station/internal/settings"
)

const settingsKey = "fallback"

// Settings is what managers edit.
type Settings struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	Shuffle bool   `json:"shuffle"`
}

// PlaylistInfo summarises the expanded playlist for API consumers.
type PlaylistInfo struct {
	Provider   string            `json:"provider"`
	ID         string            `json:"id"`
	URL        string            `json:"url"`
	Title      string            `json:"title"`
	TrackCount int               `json:"trackCount"`
	FetchedAt  time.Time         `json:"fetchedAt"`
	Tracks     []*provider.Track `json:"tracks,omitempty"`
}

// View is the full state returned by the API.
type View struct {
	Settings
	Playlist  *PlaylistInfo   `json:"playlist,omitempty"`
	Next      *provider.Track `json:"next,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt,omitzero"`
}

// persisted is the document stored under settingsKey.
type persisted struct {
	Settings
	Playlist  *provider.Playlist `json:"playlist,omitempty"`
	FetchedAt time.Time          `json:"fetchedAt,omitzero"`
	// Order is a permutation of track indexes (identity unless shuffled);
	// Cursor indexes into it and points at the track Next will return.
	Order     []int     `json:"order,omitempty"`
	Cursor    int       `json:"cursor"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// Resolver expands a playlist URL. *provider.Registry satisfies it.
type Resolver interface {
	ResolvePlaylist(ctx context.Context, input string) (*provider.Playlist, error)
}

// Manager owns the default-playlist state. It implements queue.FallbackSource.
type Manager struct {
	store    settings.Store
	resolver Resolver
	logger   *slog.Logger
	// RefreshAfter is how old the expanded playlist may get before it is
	// re-fetched in the background (so new videos show up). Default 6h.
	RefreshAfter time.Duration
	// OnChange, when set, is called (without the lock held) after every change.
	OnChange func()

	mu         sync.Mutex
	st         persisted
	configured bool // a document exists in the store
	refreshing bool
	rng        *rand.Rand
	now        func() time.Time
}

// New loads the persisted state from store.
func New(ctx context.Context, store settings.Store, resolver Resolver, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{store: store, resolver: resolver, logger: logger, RefreshAfter: 6 * time.Hour,
		rng: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 42)), now: time.Now}
	raw, err := store.Get(ctx, settingsKey)
	switch {
	case errors.Is(err, settings.ErrNotFound):
		return m, nil
	case err != nil:
		return nil, fmt.Errorf("load fallback settings: %w", err)
	}
	if err := json.Unmarshal(raw, &m.st); err != nil {
		return nil, fmt.Errorf("decode fallback settings: %w", err)
	}
	m.configured = true
	m.ensureOrderLocked()
	return m, nil
}

// Configured reports whether settings have ever been saved (from the UI or a seed).
func (m *Manager) Configured() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configured
}

// Seed applies s only when nothing has been configured yet. It lets an
// environment variable provide the initial playlist without overriding what
// managers later set from the UI.
func (m *Manager) Seed(ctx context.Context, s Settings) (View, bool, error) {
	if m.Configured() {
		return m.Get(), false, nil
	}
	v, err := m.Update(ctx, s)
	return v, err == nil, err
}

// Get returns the current state including the full track list.
func (m *Manager) Get() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewLocked(true)
}

// Update replaces the settings. A new URL (or a missing expansion) triggers a
// synchronous playlist fetch, so callers should pass a context with a timeout.
func (m *Manager) Update(ctx context.Context, s Settings) (View, error) {
	defer m.notify()
	s.URL = strings.TrimSpace(s.URL)
	if s.Enabled && s.URL == "" {
		return View{}, &ValidationError{Msg: "a playlist URL is required to enable the default playlist"}
	}

	m.mu.Lock()
	cur := m.st
	m.mu.Unlock()

	var pl *provider.Playlist
	var fetched time.Time
	sameURL := cur.Playlist != nil && cur.URL == s.URL
	switch {
	case s.URL == "":
		// Cleared: drop the expansion too.
	case sameURL:
		pl, fetched = cur.Playlist, cur.FetchedAt
	default:
		var err error
		if pl, err = m.resolver.ResolvePlaylist(ctx, s.URL); err != nil {
			return View{}, err
		}
		fetched = m.now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	reshuffle := !sameURL || m.st.Shuffle != s.Shuffle
	m.st.Settings = s
	m.st.Playlist = pl
	m.st.FetchedAt = fetched
	if reshuffle {
		m.st.Order, m.st.Cursor = nil, 0
	}
	m.ensureOrderLocked()
	m.st.UpdatedAt = m.now()
	if err := m.persistLocked(ctx); err != nil {
		return View{}, err
	}
	m.configured = true
	return m.viewLocked(true), nil
}

// Refresh re-fetches the configured playlist so newly added videos show up.
func (m *Manager) Refresh(ctx context.Context) (View, error) {
	defer m.notify()
	m.mu.Lock()
	url := m.st.URL
	m.refreshing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.refreshing = false; m.mu.Unlock() }()
	if url == "" {
		return View{}, &ValidationError{Msg: "no playlist is configured"}
	}
	pl, err := m.resolver.ResolvePlaylist(ctx, url)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.URL != url {
		return m.viewLocked(true), nil // changed underneath us; keep the newer state
	}
	// Keep the position in the playlist when the track list did not change size;
	// otherwise the permutation is meaningless and we start over.
	if m.st.Playlist == nil || len(m.st.Playlist.Tracks) != len(pl.Tracks) {
		m.st.Order, m.st.Cursor = nil, 0
	}
	m.st.Playlist = pl
	m.st.FetchedAt = m.now()
	m.ensureOrderLocked()
	if err := m.persistLocked(ctx); err != nil {
		return View{}, err
	}
	return m.viewLocked(true), nil
}

// Next implements queue.FallbackSource.
func (m *Manager) Next(ctx context.Context) (*provider.Track, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.activeLocked() {
		return nil, ""
	}
	t := m.st.Playlist.Tracks[m.st.Order[m.st.Cursor]]
	m.st.Cursor++
	if m.st.Cursor >= len(m.st.Order) {
		m.st.Cursor = 0
		if m.st.Shuffle {
			m.reshuffleLocked(t) // new order each lap, never repeating the last track first
		}
	}
	if err := m.persistLocked(ctx); err != nil {
		m.logger.Warn("persist fallback cursor", "err", err)
	}
	if m.RefreshAfter > 0 && !m.refreshing && m.now().Sub(m.st.FetchedAt) > m.RefreshAfter {
		m.refreshing = true
		go func() {
			rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := m.Refresh(rctx); err != nil {
				m.logger.Warn("refresh default playlist", "err", err)
			}
		}()
	}
	return t, m.st.Playlist.Title
}

// Peek implements queue.FallbackSource.
func (m *Manager) Peek() *queue.FallbackState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := &queue.FallbackState{Enabled: m.st.Enabled, Shuffle: m.st.Shuffle}
	if m.st.Playlist != nil {
		st.PlaylistTitle = m.st.Playlist.Title
		st.PlaylistURL = m.st.Playlist.URL
		st.TrackCount = len(m.st.Playlist.Tracks)
	}
	st.Next = m.nextLocked()
	return st
}

func (m *Manager) activeLocked() bool {
	return m.st.Enabled && m.st.Playlist != nil && len(m.st.Playlist.Tracks) > 0 && len(m.st.Order) == len(m.st.Playlist.Tracks)
}

func (m *Manager) nextLocked() *provider.Track {
	if !m.activeLocked() {
		return nil
	}
	return m.st.Playlist.Tracks[m.st.Order[m.st.Cursor]]
}

// ensureOrderLocked makes Order a valid permutation for the current playlist.
func (m *Manager) ensureOrderLocked() {
	n := 0
	if m.st.Playlist != nil {
		n = len(m.st.Playlist.Tracks)
	}
	if n == 0 {
		m.st.Order, m.st.Cursor = nil, 0
		return
	}
	valid := len(m.st.Order) == n
	if valid {
		seen := make([]bool, n)
		for _, i := range m.st.Order {
			if i < 0 || i >= n || seen[i] {
				valid = false
				break
			}
			seen[i] = true
		}
	}
	if !valid {
		m.st.Order = make([]int, n)
		for i := range m.st.Order {
			m.st.Order[i] = i
		}
		m.st.Cursor = 0
		if m.st.Shuffle {
			m.reshuffleLocked(nil)
		}
	}
	if m.st.Cursor < 0 || m.st.Cursor >= n {
		m.st.Cursor = 0
	}
}

// reshuffleLocked randomises Order; if avoid is non-nil the first track will
// differ from it (when the playlist has more than one track).
func (m *Manager) reshuffleLocked(avoid *provider.Track) {
	m.rng.Shuffle(len(m.st.Order), func(i, j int) { m.st.Order[i], m.st.Order[j] = m.st.Order[j], m.st.Order[i] })
	if avoid != nil && len(m.st.Order) > 1 && m.st.Playlist.Tracks[m.st.Order[0]] == avoid {
		j := 1 + m.rng.IntN(len(m.st.Order)-1)
		m.st.Order[0], m.st.Order[j] = m.st.Order[j], m.st.Order[0]
	}
}

func (m *Manager) persistLocked(ctx context.Context) error {
	b, err := json.Marshal(m.st)
	if err != nil {
		return err
	}
	return m.store.Set(ctx, settingsKey, b)
}

func (m *Manager) viewLocked(withTracks bool) View {
	v := View{Settings: m.st.Settings, UpdatedAt: m.st.UpdatedAt, Next: m.nextLocked()}
	if pl := m.st.Playlist; pl != nil {
		v.Playlist = &PlaylistInfo{Provider: pl.Provider, ID: pl.ID, URL: pl.URL, Title: pl.Title, TrackCount: len(pl.Tracks), FetchedAt: m.st.FetchedAt}
		if withTracks {
			v.Playlist.Tracks = pl.Tracks
		}
	}
	return v
}

func (m *Manager) notify() {
	if m.OnChange != nil {
		m.OnChange()
	}
}

// ValidationError reports bad settings input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }
