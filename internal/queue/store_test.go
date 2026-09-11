package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"central-station/internal/db"
)

// storeConformance exercises a Store the way the Service uses it.
func storeConformance(t *testing.T, st Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	p, err := st.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 0 || p.Current != nil || p.Status != StatusIdle {
		t.Fatalf("fresh store not empty: %+v", p)
	}

	a := &Item{ID: "11111111-1111-4111-8111-111111111111", Track: track("a"), AddedByID: "u1", AddedByName: "Ann", AddedAt: now}
	b := &Item{ID: "22222222-2222-4222-8222-222222222222", Track: track("b"), AddedByID: "u2", AddedByName: "Bob", AddedAt: now}
	c := &Item{ID: "33333333-3333-4333-8333-333333333333", Track: track("c"), AddedByID: "u1", AddedByName: "Ann", AddedAt: now}
	for _, it := range []*Item{a, b, c} {
		if err := st.Insert(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	ids := func() []string {
		p, err := st.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(p.Items))
		for i, it := range p.Items {
			out[i] = it.Track.ID
		}
		return out
	}
	if got := ids(); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("after insert: %v", got)
	}

	if err := st.Reorder(ctx, []string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if got := ids(); got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("after reorder: %v", got)
	}

	// Promote c into the current slot (no history yet).
	if err := st.Transition(ctx, nil, c, StatusPlaying, c, now); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Load(ctx)
	if p.Current == nil || p.Current.ID != c.ID || p.Status != StatusPlaying || !p.StartedAt.Equal(now) || len(p.Items) != 2 {
		t.Fatalf("after transition: cur=%+v status=%s started=%s items=%d", p.Current, p.Status, p.StartedAt, len(p.Items))
	}
	if p.Current.Track.Title != "c" || p.Current.AddedByName != "Ann" {
		t.Fatalf("current item lost fields: %+v", p.Current)
	}

	if err := st.SetPlayback(ctx, StatusPaused, c, now); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.Load(ctx); p.Status != StatusPaused {
		t.Fatalf("pause not persisted: %s", p.Status)
	}

	// c ends (skipped), a becomes current.
	ended := &HistoryEntry{Item: c, StartedAt: now, EndedAt: now.Add(time.Minute), EndReason: EndSkipped, EndedByID: "mgr"}
	if err := st.Transition(ctx, ended, a, StatusPlaying, a, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Load(ctx)
	if p.Current.ID != a.ID || len(p.Items) != 1 || p.Items[0].ID != b.ID {
		t.Fatalf("after second transition: %+v", p)
	}

	if err := st.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	// a fails, queue empty -> idle.
	failed := &HistoryEntry{Item: a, StartedAt: now, EndedAt: now.Add(2 * time.Minute), EndReason: EndError, EndedByID: "player", Error: "boom"}
	if err := st.Transition(ctx, failed, nil, StatusIdle, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Load(ctx)
	if p.Current != nil || p.Status != StatusIdle || len(p.Items) != 0 || !p.StartedAt.IsZero() {
		t.Fatalf("after idle transition: %+v", p)
	}

	h, err := st.History(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].Item.ID != a.ID || h[0].EndReason != EndError || h[0].Error != "boom" || h[1].Item.ID != c.ID || h[1].EndedByID != "mgr" {
		t.Fatalf("history: %+v %+v", h[0], h[1])
	}
	if !h[1].EndedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("history timestamps: %s", h[1].EndedAt)
	}
	if h, _ = st.History(ctx, 1); len(h) != 1 || h[0].Item.ID != a.ID {
		t.Fatalf("history limit: %+v", h)
	}
}

func TestMemoryStoreConformance(t *testing.T) {
	storeConformance(t, NewMemoryStore())
}

// postgresPool returns a migrated pool with empty tables, or skips.
func postgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users, queue_items, playback, history`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPostgresStoreConformance(t *testing.T) {
	pool := postgresPool(t)
	storeConformance(t, NewPostgresStore(pool))
}

func TestServiceReloadsFromPostgres(t *testing.T) {
	pool := postgresPool(t)
	ctx := context.Background()
	q, err := New(ctx, NewPostgresStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	cur := mustAdd(t, q, "x", "u1", "Ann")
	mustAdd(t, q, "y", "u1", "Ann")
	if err := q.Pause(ctx); err != nil {
		t.Fatal(err)
	}

	// A fresh Service (simulating a restart) sees the same state.
	q2, err := New(ctx, NewPostgresStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	s := q2.Snapshot()
	if s.Status != StatusPaused || s.Current == nil || s.Current.ID != cur.ID || len(s.Items) != 1 || s.Items[0].Track.ID != "y" {
		t.Fatalf("reloaded state: %+v", s)
	}
	if _, err := q2.Skip(ctx, "mgr"); err != nil {
		t.Fatal(err)
	}
	h, _ := q2.History(ctx, 5)
	if len(h) != 1 || h[0].Item.ID != cur.ID {
		t.Fatalf("history after reload: %+v", h)
	}
}
