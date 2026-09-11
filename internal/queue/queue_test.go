package queue

import (
	"context"
	"errors"
	"testing"

	"central-station/internal/provider"
)

var ctx = context.Background()

func track(id string) *provider.Track {
	return &provider.Track{Provider: "youtube", ID: id, Title: id}
}

func mustAdd(t *testing.T, q *Service, id, userID, name string) *Item {
	t.Helper()
	it, err := q.Add(ctx, track(id), userID, name)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestAddPromotesFirstItem(t *testing.T) {
	q := NewMemory()
	a := mustAdd(t, q, "a", "u1", "Ann")
	b := mustAdd(t, q, "b", "u2", "Bob")
	s := q.Snapshot()
	if s.Status != StatusPlaying || s.Current == nil || s.Current.ID != a.ID {
		t.Fatalf("state = %+v", s)
	}
	if len(s.Items) != 1 || s.Items[0].ID != b.ID {
		t.Fatalf("items = %+v", s.Items)
	}
}

func TestSkipAndHistory(t *testing.T) {
	q := NewMemory()
	a := mustAdd(t, q, "a", "u1", "Ann")
	b := mustAdd(t, q, "b", "u2", "Bob")
	next, err := q.Skip(ctx, "mgr")
	if err != nil || next == nil || next.ID != b.ID {
		t.Fatalf("skip: next=%+v err=%v", next, err)
	}
	h, _ := q.History(ctx, 10)
	if len(h) != 1 || h[0].Item.ID != a.ID || h[0].EndReason != EndSkipped || h[0].EndedByID != "mgr" {
		t.Fatalf("history = %+v", h)
	}
	if _, err := q.Complete(ctx, "player"); err != nil {
		t.Fatal(err)
	}
	if s := q.Snapshot(); s.Status != StatusIdle || s.Current != nil {
		t.Fatalf("expected idle, got %+v", s)
	}
	if _, err := q.Skip(ctx, "mgr"); !errors.Is(err, ErrNotPlaying) {
		t.Fatalf("want ErrNotPlaying, got %v", err)
	}
	if got, _ := q.History(ctx, 1); len(got) != 1 || got[0].Item.ID != b.ID {
		t.Fatalf("history newest first: %+v", got)
	}
}

func TestPauseResume(t *testing.T) {
	q := NewMemory()
	if err := q.Pause(ctx); !errors.Is(err, ErrNotPlaying) {
		t.Fatal(err)
	}
	mustAdd(t, q, "a", "u1", "Ann")
	if err := q.Pause(ctx); err != nil || q.Snapshot().Status != StatusPaused {
		t.Fatalf("pause: %v", err)
	}
	if err := q.Resume(ctx); err != nil || q.Snapshot().Status != StatusPlaying {
		t.Fatalf("resume: %v", err)
	}
}

func TestRemoveAndMove(t *testing.T) {
	q := NewMemory()
	cur := mustAdd(t, q, "cur", "u", "U")
	a := mustAdd(t, q, "a", "u", "U")
	b := mustAdd(t, q, "b", "u", "U")
	c := mustAdd(t, q, "c", "u", "U")

	if _, err := q.Remove(ctx, cur.ID); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("current must not be removable: %v", err)
	}
	if err := q.Move(ctx, c.ID, 0); err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		var out []string
		for _, it := range q.Snapshot().Items {
			out = append(out, it.Track.ID)
		}
		return out
	}
	if got := ids(); got[0] != "c" || got[1] != "a" || got[2] != "b" {
		t.Fatalf("after move: %v", got)
	}
	if err := q.Move(ctx, a.ID, 5); !errors.Is(err, ErrBadPosition) {
		t.Fatalf("want ErrBadPosition, got %v", err)
	}
	if _, err := q.Remove(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := ids(); len(got) != 2 || got[1] != "a" {
		t.Fatalf("after remove: %v", got)
	}
	_ = a
}
