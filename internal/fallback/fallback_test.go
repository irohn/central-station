package fallback

import (
	"context"
	"errors"
	"testing"
	"time"

	"central-station/internal/provider"
	"central-station/internal/settings"
)

type fakeResolver struct {
	calls int
	lists map[string]*provider.Playlist
}

func (f *fakeResolver) ResolvePlaylist(_ context.Context, input string) (*provider.Playlist, error) {
	f.calls++
	pl, ok := f.lists[input]
	if !ok {
		return nil, provider.ErrNotFound
	}
	cp := *pl
	return &cp, nil
}

func list(id string, n int) *provider.Playlist {
	pl := &provider.Playlist{Provider: "youtube", ID: id, URL: "https://www.youtube.com/playlist?list=" + id, Title: "List " + id}
	for i := 0; i < n; i++ {
		pl.Tracks = append(pl.Tracks, &provider.Track{Provider: "youtube", ID: id + string(rune('a'+i)), Title: id + string(rune('a'+i))})
	}
	return pl
}

func newManager(t *testing.T, store settings.Store, r *fakeResolver) *Manager {
	t.Helper()
	m, err := New(context.Background(), store, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdateResolvesAndPlaysInOrder(t *testing.T) {
	ctx := context.Background()
	store := settings.NewMemory()
	r := &fakeResolver{lists: map[string]*provider.Playlist{"u1": list("PL1", 3)}}
	m := newManager(t, store, r)
	if m.Configured() {
		t.Fatal("fresh manager should not be configured")
	}
	if tr, _ := m.Next(ctx); tr != nil {
		t.Fatal("nothing configured, Next should be nil")
	}

	v, err := m.Update(ctx, Settings{URL: "u1", Enabled: true})
	if err != nil || v.Playlist == nil || v.Playlist.TrackCount != 3 || v.Next.ID != "PL1a" {
		t.Fatalf("update: %v %+v", err, v)
	}
	var got []string
	for i := 0; i < 4; i++ {
		tr, origin := m.Next(ctx)
		if origin != "List PL1" {
			t.Fatalf("origin = %q", origin)
		}
		got = append(got, tr.ID)
	}
	if want := "PL1a PL1b PL1c PL1a"; joinIDs(got) != want {
		t.Fatalf("order = %v", got)
	}
	if st := m.Peek(); !st.Enabled || st.TrackCount != 3 || st.Next.ID != "PL1b" || st.PlaylistTitle != "List PL1" {
		t.Fatalf("peek = %+v", st)
	}

	// Same URL again: no re-fetch, position kept.
	before := r.calls
	if _, err := m.Update(ctx, Settings{URL: "u1", Enabled: true}); err != nil || r.calls != before {
		t.Fatalf("same url refetched: %v calls=%d", err, r.calls)
	}
	if tr, _ := m.Next(ctx); tr.ID != "PL1b" {
		t.Fatalf("cursor lost: %s", tr.ID)
	}

	// Persisted: a new manager on the same store continues where we left off.
	m2 := newManager(t, store, &fakeResolver{})
	if !m2.Configured() {
		t.Fatal("should be configured after load")
	}
	if tr, _ := m2.Next(ctx); tr.ID != "PL1c" {
		t.Fatalf("reload cursor: %s", tr.ID)
	}
}

func TestShuffleCoversAllTracksWithoutImmediateRepeat(t *testing.T) {
	ctx := context.Background()
	r := &fakeResolver{lists: map[string]*provider.Playlist{"u": list("PL", 6)}}
	m := newManager(t, settings.NewMemory(), r)
	if _, err := m.Update(ctx, Settings{URL: "u", Enabled: true, Shuffle: true}); err != nil {
		t.Fatal(err)
	}
	var prev string
	for lap := 0; lap < 20; lap++ {
		seen := map[string]bool{}
		for i := 0; i < 6; i++ {
			tr, _ := m.Next(ctx)
			if seen[tr.ID] {
				t.Fatalf("lap %d repeated %s", lap, tr.ID)
			}
			if i == 0 && tr.ID == prev {
				t.Fatalf("lap %d started with the track that just played", lap)
			}
			seen[tr.ID] = true
			prev = tr.ID
		}
	}
}

func TestDisableAndValidation(t *testing.T) {
	ctx := context.Background()
	r := &fakeResolver{lists: map[string]*provider.Playlist{"u": list("PL", 2)}}
	m := newManager(t, settings.NewMemory(), r)
	if _, err := m.Update(ctx, Settings{Enabled: true}); err == nil {
		t.Fatal("enabled without url should fail")
	}
	var ve *ValidationError
	if _, err := m.Update(ctx, Settings{Enabled: true}); !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if _, err := m.Update(ctx, Settings{URL: "missing", Enabled: true}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("resolver error should propagate: %v", err)
	}
	if _, err := m.Update(ctx, Settings{URL: "u", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(ctx, Settings{URL: "u", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if tr, _ := m.Next(ctx); tr != nil {
		t.Fatal("disabled source should return nil")
	}
	if st := m.Peek(); st.Enabled || st.Next != nil || st.TrackCount != 2 {
		t.Fatalf("peek disabled = %+v", st)
	}
	// Clearing the URL drops the expansion.
	v, err := m.Update(ctx, Settings{})
	if err != nil || v.Playlist != nil {
		t.Fatalf("cleared: %v %+v", err, v)
	}
}

func TestRefreshKeepsCursorWhenSizeUnchanged(t *testing.T) {
	ctx := context.Background()
	r := &fakeResolver{lists: map[string]*provider.Playlist{"u": list("PL", 3)}}
	m := newManager(t, settings.NewMemory(), r)
	if _, err := m.Update(ctx, Settings{URL: "u", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m.Next(ctx) // cursor -> 1
	if _, err := m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if tr, _ := m.Next(ctx); tr.ID != "PLb" {
		t.Fatalf("cursor after same-size refresh: %s", tr.ID)
	}
	r.lists["u"] = list("PL", 5)
	if v, err := m.Refresh(ctx); err != nil || v.Playlist.TrackCount != 5 {
		t.Fatalf("grown refresh: %v %+v", err, v)
	}
	if tr, _ := m.Next(ctx); tr.ID != "PLa" {
		t.Fatalf("cursor after resize should restart: %s", tr.ID)
	}
}

func TestSeedOnlyWhenUnconfigured(t *testing.T) {
	ctx := context.Background()
	r := &fakeResolver{lists: map[string]*provider.Playlist{"env": list("ENV", 2), "ui": list("UI", 2)}}
	store := settings.NewMemory()
	m := newManager(t, store, r)
	if _, seeded, err := m.Seed(ctx, Settings{URL: "env", Enabled: true, Shuffle: true}); err != nil || !seeded {
		t.Fatalf("first seed: %v %v", err, seeded)
	}
	if _, err := m.Update(ctx, Settings{URL: "ui", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m2 := newManager(t, store, r)
	if v, seeded, err := m2.Seed(ctx, Settings{URL: "env", Enabled: true}); err != nil || seeded || v.URL != "ui" {
		t.Fatalf("seed must not override UI setting: %v %v %+v", err, seeded, v)
	}
}

func TestStaleTriggersBackgroundRefresh(t *testing.T) {
	ctx := context.Background()
	r := &fakeResolver{lists: map[string]*provider.Playlist{"u": list("PL", 2)}}
	m := newManager(t, settings.NewMemory(), r)
	if _, err := m.Update(ctx, Settings{URL: "u", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Now().Add(7 * time.Hour) }
	calls := r.calls
	m.Next(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for r.calls == calls && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if r.calls == calls {
		t.Fatal("stale playlist was not refreshed")
	}
}

func joinIDs(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += id
	}
	return out
}
