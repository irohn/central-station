// Package queue holds the office play queue, the current playback state and history.
//
// The Service owns the queue logic and an in-memory mirror of the state; a
// Store persists it (Postgres in production, memory in tests). All mutations
// are serialised by the Service mutex and written through to the Store before
// the mirror is updated, so a failed write leaves the mirror unchanged.
package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"central-station/internal/provider"
)

// Status of the logical player.
type Status string

const (
	StatusIdle    Status = "idle"    // nothing current
	StatusPlaying Status = "playing" // current item is playing
	StatusPaused  Status = "paused"  // current item is paused
)

// Item is a track queued by a user.
type Item struct {
	ID          string          `json:"id"`
	Track       *provider.Track `json:"track"`
	AddedByID   string          `json:"addedById"`
	AddedByName string          `json:"addedByName"`
	AddedAt     time.Time       `json:"addedAt"`
}

// EndReason explains why an item left the current slot.
type EndReason string

const (
	EndFinished EndReason = "finished"
	EndSkipped  EndReason = "skipped"
	EndError    EndReason = "error" // the player could not play it
)

// HistoryEntry records a track that has played (or was skipped).
type HistoryEntry struct {
	Item      *Item     `json:"item"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	EndReason EndReason `json:"endReason"`
	EndedByID string    `json:"endedById,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// State is a snapshot of the queue.
type State struct {
	Status    Status    `json:"status"`
	Current   *Item     `json:"current"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	Items     []*Item   `json:"items"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var (
	ErrItemNotFound = errors.New("queue item not found")
	ErrNotPlaying   = errors.New("nothing is playing")
	ErrBadPosition  = errors.New("position out of range")
)

// Service is the thread-safe queue.
type Service struct {
	store Store

	mu        sync.RWMutex
	items     []*Item
	current   *Item
	startedAt time.Time
	status    Status
	updatedAt time.Time
	now       func() time.Time

	// OnChange, when set, is called (without the lock held) after every mutation.
	OnChange func()
}

// New creates a queue backed by store and loads the persisted state.
func New(ctx context.Context, store Store) (*Service, error) {
	s := &Service{store: store, status: StatusIdle, now: time.Now, updatedAt: time.Now()}
	p, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load queue: %w", err)
	}
	s.items = p.Items
	s.current = p.Current
	s.startedAt = p.StartedAt
	s.status = p.Status
	if s.current == nil {
		s.status = StatusIdle
	}
	return s, nil
}

// NewMemory creates an empty in-memory queue (tests, or no DATABASE_URL).
func NewMemory() *Service {
	s, _ := New(context.Background(), NewMemoryStore())
	return s
}

// Snapshot returns a copy of the current state.
func (s *Service) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]*Item, len(s.items))
	copy(items, s.items)
	return State{Status: s.status, Current: s.current, StartedAt: s.startedAt, Items: items, UpdatedAt: s.updatedAt}
}

// Add appends a track. If nothing is current it becomes current and starts playing.
func (s *Service) Add(ctx context.Context, track *provider.Track, userID, userName string) (*Item, error) {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	it := &Item{ID: uuid.NewString(), Track: track, AddedByID: userID, AddedByName: userName, AddedAt: s.now()}
	if err := s.store.Insert(ctx, it); err != nil {
		return nil, err
	}
	s.items = append(s.items, it)
	s.touch()
	if s.current == nil {
		if err := s.transitionLocked(ctx, nil); err != nil {
			return it, err
		}
	}
	return it, nil
}

// Get returns a queued (not current) item.
func (s *Service) Get(id string) (*Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.items {
		if it.ID == id {
			return it, nil
		}
	}
	return nil, ErrItemNotFound
}

// Current returns the item in the current slot, if any.
func (s *Service) Current() *Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Remove drops a queued item. The current item cannot be removed; use Skip.
func (s *Service) Remove(ctx context.Context, id string) (*Item, error) {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range s.items {
		if it.ID == id {
			if err := s.store.Delete(ctx, id); err != nil {
				return nil, err
			}
			s.items = append(s.items[:i], s.items[i+1:]...)
			s.touch()
			return it, nil
		}
	}
	return nil, ErrItemNotFound
}

// Move places item id at the zero-based position within the queue.
func (s *Service) Move(ctx context.Context, id string, position int) error {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, it := range s.items {
		if it.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrItemNotFound
	}
	if position < 0 || position >= len(s.items) {
		return ErrBadPosition
	}
	next := make([]*Item, 0, len(s.items))
	next = append(next, s.items[:idx]...)
	next = append(next, s.items[idx+1:]...)
	next = append(next[:position], append([]*Item{s.items[idx]}, next[position:]...)...)
	ids := make([]string, len(next))
	for i, it := range next {
		ids[i] = it.ID
	}
	if err := s.store.Reorder(ctx, ids); err != nil {
		return err
	}
	s.items = next
	s.touch()
	return nil
}

// Skip ends the current item and promotes the next one.
func (s *Service) Skip(ctx context.Context, byUserID string) (*Item, error) {
	return s.end(ctx, EndSkipped, byUserID, "")
}

// Complete marks the current item as finished (reported by the player) and advances.
func (s *Service) Complete(ctx context.Context, byUserID string) (*Item, error) {
	return s.end(ctx, EndFinished, byUserID, "")
}

// Fail records that the player could not play the current item and advances.
func (s *Service) Fail(ctx context.Context, byUserID, errMsg string) (*Item, error) {
	return s.end(ctx, EndError, byUserID, errMsg)
}

func (s *Service) end(ctx context.Context, reason EndReason, byUserID, errMsg string) (*Item, error) {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return nil, ErrNotPlaying
	}
	ended := &HistoryEntry{
		Item: s.current, StartedAt: s.startedAt, EndedAt: s.now(), EndReason: reason, EndedByID: byUserID, Error: errMsg,
	}
	if err := s.transitionLocked(ctx, ended); err != nil {
		return nil, err
	}
	return s.current, nil
}

// Pause pauses the current item.
func (s *Service) Pause(ctx context.Context) error {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		return ErrNotPlaying
	}
	if err := s.store.SetPlayback(ctx, StatusPaused, s.current, s.startedAt); err != nil {
		return err
	}
	s.status = StatusPaused
	s.touch()
	return nil
}

// Resume resumes a paused item, or starts the next queued item when idle.
func (s *Service) Resume(ctx context.Context) error {
	defer s.notify()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil {
		if len(s.items) == 0 {
			return ErrNotPlaying
		}
		return s.transitionLocked(ctx, nil)
	}
	if err := s.store.SetPlayback(ctx, StatusPlaying, s.current, s.startedAt); err != nil {
		return err
	}
	s.status = StatusPlaying
	s.touch()
	return nil
}

// History returns the most recent entries, newest first.
func (s *Service) History(ctx context.Context, limit int) ([]*HistoryEntry, error) {
	return s.store.History(ctx, limit)
}

// transitionLocked records ended (if any) and promotes the head of the queue
// into the current slot, persisting both atomically.
func (s *Service) transitionLocked(ctx context.Context, ended *HistoryEntry) error {
	var next *Item
	status := StatusIdle
	var startedAt time.Time
	if len(s.items) > 0 {
		next = s.items[0]
		status = StatusPlaying
		startedAt = s.now()
	}
	if err := s.store.Transition(ctx, ended, next, status, next, startedAt); err != nil {
		return err
	}
	if next != nil {
		s.items = s.items[1:]
	}
	s.current = next
	s.startedAt = startedAt
	s.status = status
	s.touch()
	return nil
}

func (s *Service) touch() { s.updatedAt = s.now() }

// notify runs the OnChange hook. Call it deferred *before* taking the lock so
// that it runs after the lock is released.
func (s *Service) notify() {
	if s.OnChange != nil {
		s.OnChange()
	}
}
