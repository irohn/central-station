package queue

import (
	"context"
	"testing"

	"central-station/internal/provider"
)

type stubFallback struct {
	tracks []*provider.Track
	i      int
}

func (s *stubFallback) Next(context.Context) (*provider.Track, string) {
	if len(s.tracks) == 0 {
		return nil, ""
	}
	t := s.tracks[s.i%len(s.tracks)]
	s.i++
	return t, "Office list"
}

func (s *stubFallback) Peek() *FallbackState {
	st := &FallbackState{Enabled: len(s.tracks) > 0, TrackCount: len(s.tracks), PlaylistTitle: "Office list"}
	if len(s.tracks) > 0 {
		st.Next = s.tracks[s.i%len(s.tracks)]
	}
	return st
}

func TestFallbackFillsIdleQueue(t *testing.T) {
	q := NewMemory()
	fb := &stubFallback{tracks: []*provider.Track{track("f1"), track("f2")}}
	q.SetFallback(fb)

	if err := q.Nudge(ctx); err != nil {
		t.Fatal(err)
	}
	s := q.Snapshot()
	if s.Status != StatusPlaying || s.Current == nil || s.Current.Track.ID != "f1" || !s.Current.IsFallback() || s.Current.AddedByName != "Office list" {
		t.Fatalf("after nudge: %+v", s.Current)
	}
	if len(s.Items) != 0 || s.Fallback == nil || s.Fallback.Next.ID != "f2" {
		t.Fatalf("snapshot: items=%d fallback=%+v", len(s.Items), s.Fallback)
	}

	// A user track queues behind the fallback track and plays after it.
	u := mustAdd(t, q, "u1", "u", "Uma")
	if cur := q.Current(); cur.Track.ID != "f1" {
		t.Fatalf("user add interrupted fallback: %s", cur.Track.ID)
	}
	if _, err := q.Complete(ctx, "player"); err != nil {
		t.Fatal(err)
	}
	if cur := q.Current(); cur.ID != u.ID {
		t.Fatalf("user track should follow: %+v", cur)
	}
	// Queue drained again: back to the fallback, which continues from f2.
	if _, err := q.Skip(ctx, "mgr"); err != nil {
		t.Fatal(err)
	}
	if cur := q.Current(); !cur.IsFallback() || cur.Track.ID != "f2" {
		t.Fatalf("after drain: %+v", cur)
	}
	h, _ := q.History(ctx, 10)
	if len(h) != 2 || !h[1].Item.IsFallback() || h[1].EndReason != EndFinished || h[0].Item.ID != u.ID {
		t.Fatalf("history: %+v", h)
	}
}

func TestFallbackEmptyLeavesIdle(t *testing.T) {
	q := NewMemory()
	q.SetFallback(&stubFallback{})
	if err := q.Nudge(ctx); err != nil {
		t.Fatal(err)
	}
	if s := q.Snapshot(); s.Status != StatusIdle || s.Current != nil {
		t.Fatalf("expected idle: %+v", s)
	}
	if err := q.Resume(ctx); err != ErrNotPlaying {
		t.Fatalf("resume idle with empty fallback: %v", err)
	}
	// Nudge is a no-op while something is queued or playing.
	a := mustAdd(t, q, "a", "u", "Ann")
	if err := q.Nudge(ctx); err != nil || q.Current().ID != a.ID {
		t.Fatalf("nudge while playing: %v", err)
	}
}

func TestResumeIdleStartsFallback(t *testing.T) {
	q := NewMemory()
	q.SetFallback(&stubFallback{tracks: []*provider.Track{track("f1")}})
	if err := q.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if cur := q.Current(); cur == nil || cur.Track.ID != "f1" {
		t.Fatalf("resume should start fallback: %+v", cur)
	}
}
