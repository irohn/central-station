package queue

import (
	"math/rand/v2"
	"testing"

	"central-station/internal/provider"
)

func playlist(id string, n int) *provider.Playlist {
	pl := &provider.Playlist{Provider: "youtube", ID: id, URL: "https://www.youtube.com/playlist?list=" + id, Title: "list " + id}
	for i := 0; i < n; i++ {
		pl.Tracks = append(pl.Tracks, track(id+string(rune('a'+i))))
	}
	return pl
}

func ids(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Track.ID
	}
	return out
}

func TestPlaylistPriorityOrder(t *testing.T) {
	q := NewMemory()
	q.SetFallback(&stubFallback{tracks: []*provider.Track{track("fb")}})

	// Idle: adding a playlist starts it right away (ordered, not shuffled).
	p1, err := q.AddPlaylist(ctx, playlist("P1", 2), "u1", "Ann", false)
	if err != nil {
		t.Fatal(err)
	}
	s := q.Snapshot()
	if s.Current == nil || s.Current.Track.ID != "P1a" || s.Current.Source != SourcePlaylist || s.Current.PlaylistID != p1.ID || s.Current.PlaylistTitle != "list P1" {
		t.Fatalf("current = %+v", s.Current)
	}
	if len(s.Playlists) != 1 || !s.Playlists[0].Playing || s.Playlists[0].Played != 1 || s.Playlists[0].Next.ID != "P1b" {
		t.Fatalf("summary = %+v", s.Playlists)
	}

	// A song and a second playlist arrive while P1a plays.
	song := mustAdd(t, q, "s1", "u2", "Bob")
	if _, err := q.AddPlaylist(ctx, playlist("P2", 1), "u2", "Bob", false); err != nil {
		t.Fatal(err)
	}
	if cur := q.Current(); cur.Track.ID != "P1a" {
		t.Fatalf("additions must not interrupt: %s", cur.Track.ID)
	}

	// Expected order: song, rest of P1, then P2, then the fallback.
	var order []string
	for i := 0; i < 4; i++ {
		if _, err := q.Complete(ctx, "player"); err != nil {
			t.Fatal(err)
		}
		order = append(order, q.Current().Track.ID)
	}
	if got := joinIDs(order); got != "s1 P1b P2a fb" {
		t.Fatalf("play order = %q", got)
	}
	if cur := q.Current(); cur.ID == song.ID || cur.Source != SourceFallback {
		t.Fatalf("expected fallback at the end: %+v", cur)
	}
	if s := q.Snapshot(); len(s.Playlists) != 0 {
		t.Fatalf("played-through playlists must be dropped: %+v", s.Playlists)
	}
}

func TestRemovePlaylistSkipsItsTrack(t *testing.T) {
	q := NewMemory()
	p1, _ := q.AddPlaylist(ctx, playlist("P1", 3), "u1", "Ann", false)
	p2, _ := q.AddPlaylist(ctx, playlist("P2", 1), "u2", "Bob", false)
	if cur := q.Current(); cur.PlaylistID != p1.ID {
		t.Fatalf("current = %+v", cur)
	}
	// Removing the playing playlist skips to the next playlist.
	if _, err := q.RemovePlaylist(ctx, p1.ID, "mgr"); err != nil {
		t.Fatal(err)
	}
	if cur := q.Current(); cur == nil || cur.PlaylistID != p2.ID {
		t.Fatalf("after remove: %+v", cur)
	}
	h, _ := q.History(ctx, 5)
	if len(h) != 1 || h[0].EndReason != EndSkipped || h[0].EndedByID != "mgr" || h[0].Item.Track.ID != "P1a" {
		t.Fatalf("history = %+v", h)
	}
	// Removing a waiting playlist does not touch playback.
	p3, _ := q.AddPlaylist(ctx, playlist("P3", 1), "u1", "Ann", false)
	if _, err := q.RemovePlaylist(ctx, p3.ID, "mgr"); err != nil {
		t.Fatal(err)
	}
	if cur := q.Current(); cur.PlaylistID != p2.ID {
		t.Fatalf("waiting removal changed playback: %+v", cur)
	}
	if _, err := q.RemovePlaylist(ctx, "nope", "mgr"); err != ErrPlaylistNotFound {
		t.Fatalf("unknown: %v", err)
	}
	// Last playlist removed while playing and nothing else -> idle.
	if _, err := q.RemovePlaylist(ctx, p2.ID, "mgr"); err != nil {
		t.Fatal(err)
	}
	if s := q.Snapshot(); s.Status != StatusIdle || s.Current != nil || len(s.Playlists) != 0 {
		t.Fatalf("expected idle: %+v", s)
	}
}

func TestShuffleAndToggle(t *testing.T) {
	q := NewMemory()
	q.SetRandom(rand.New(rand.NewPCG(1, 2)))
	pl, _ := q.AddPlaylist(ctx, playlist("P", 8), "u1", "Ann", true)
	got, _ := q.GetPlaylist(pl.ID)
	identity := true
	seen := map[int]bool{}
	for i, v := range got.Order {
		if v != i {
			identity = false
		}
		seen[v] = true
	}
	if identity || len(seen) != 8 {
		t.Fatalf("shuffled order should be a non-identity permutation: %v", got.Order)
	}
	// Play two, then switch shuffle off: the rest continues in playlist order,
	// without repeating what already played.
	played := []int{got.Order[0]}
	q.Complete(ctx, "player")
	played = append(played, got.Order[1])
	if _, err := q.SetPlaylistShuffle(ctx, pl.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = q.GetPlaylist(pl.ID)
	if got.Shuffle || got.Order[0] != played[0] || got.Order[1] != played[1] {
		t.Fatalf("played prefix changed: %v (played %v)", got.Order, played)
	}
	rest := got.Order[got.Cursor:]
	for i := 1; i < len(rest); i++ {
		if rest[i] < rest[i-1] {
			t.Fatalf("rest not in playlist order: %v", rest)
		}
	}
	if _, err := q.SetPlaylistShuffle(ctx, pl.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ = q.GetPlaylist(pl.ID)
	if !got.Shuffle || got.Order[0] != played[0] || got.Order[1] != played[1] || len(got.Order) != 8 {
		t.Fatalf("re-shuffle broke the order: %v", got.Order)
	}
}

func TestAddPlaylistRejectsEmpty(t *testing.T) {
	q := NewMemory()
	if _, err := q.AddPlaylist(ctx, &provider.Playlist{ID: "x"}, "u", "U", true); err == nil {
		t.Fatal("empty playlist accepted")
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
