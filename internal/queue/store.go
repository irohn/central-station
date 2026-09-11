package queue

import (
	"context"
	"sync"
	"time"
)

// Store persists queue state. The Service keeps an in-memory mirror and
// serialises all calls, so implementations need no locking of their own
// beyond what makes each call atomic.
type Store interface {
	// Load returns queued items in order plus the playback row.
	Load(ctx context.Context) (*Persisted, error)
	// Insert appends an item to the end of the queue.
	Insert(ctx context.Context, it *Item) error
	// Delete removes a queued item.
	Delete(ctx context.Context, id string) error
	// Reorder rewrites queue positions to match ids.
	Reorder(ctx context.Context, ids []string) error
	// SetPlayback stores the playback row.
	SetPlayback(ctx context.Context, status Status, current *Item, startedAt time.Time) error
	// Transition atomically records ended (if non-nil) in history, removes
	// next (if non-nil) from the queue, and stores the new playback row.
	Transition(ctx context.Context, ended *HistoryEntry, next *Item, status Status, current *Item, startedAt time.Time) error
	// History returns the newest entries first, at most limit.
	History(ctx context.Context, limit int) ([]*HistoryEntry, error)
}

// Persisted is what Load returns.
type Persisted struct {
	Items     []*Item
	Status    Status
	Current   *Item
	StartedAt time.Time
}

// MemoryStore is an in-memory Store for tests and for running without a database.
type MemoryStore struct {
	mu         sync.Mutex
	items      []*Item
	status     Status
	current    *Item
	startedAt  time.Time
	history    []*HistoryEntry
	maxHistory int
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{status: StatusIdle, maxHistory: 500}
}

func (m *MemoryStore) Load(context.Context) (*Persisted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*Item, len(m.items))
	copy(items, m.items)
	return &Persisted{Items: items, Status: m.status, Current: m.current, StartedAt: m.startedAt}, nil
}

func (m *MemoryStore) Insert(_ context.Context, it *Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, it)
	return nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteLocked(id)
	return nil
}

func (m *MemoryStore) deleteLocked(id string) {
	for i, it := range m.items {
		if it.ID == id {
			m.items = append(m.items[:i], m.items[i+1:]...)
			return
		}
	}
}

func (m *MemoryStore) Reorder(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	byID := map[string]*Item{}
	for _, it := range m.items {
		byID[it.ID] = it
	}
	out := make([]*Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	m.items = out
	return nil
}

func (m *MemoryStore) SetPlayback(_ context.Context, status Status, current *Item, startedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status, m.current, m.startedAt = status, current, startedAt
	return nil
}

func (m *MemoryStore) Transition(_ context.Context, ended *HistoryEntry, next *Item, status Status, current *Item, startedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ended != nil {
		m.history = append(m.history, ended)
		if len(m.history) > m.maxHistory {
			m.history = m.history[len(m.history)-m.maxHistory:]
		}
	}
	if next != nil {
		m.deleteLocked(next.ID)
	}
	m.status, m.current, m.startedAt = status, current, startedAt
	return nil
}

func (m *MemoryStore) History(_ context.Context, limit int) ([]*HistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.history)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]*HistoryEntry, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, m.history[i])
	}
	return out, nil
}
