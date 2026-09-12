package zone

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Memory is an in-memory Store.
type Memory struct {
	mu    sync.Mutex
	zones map[string]*Zone
	now   func() time.Time
}

// NewMemory creates an empty store.
func NewMemory() *Memory { return &Memory{zones: map[string]*Zone{}, now: time.Now} }

func (m *Memory) List(context.Context) ([]*Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Zone, 0, len(m.zones))
	for _, z := range m.zones {
		cp := *z
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *Memory) Get(_ context.Context, id string) (*Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, ok := m.zones[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *z
	return &cp, nil
}

func (m *Memory) Create(_ context.Context, z *Zone, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *z
	cp.tokenHash = tokenHash
	cp.HasToken = tokenHash != ""
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = m.now()
	}
	cp.UpdatedAt = cp.CreatedAt
	m.zones[cp.ID] = &cp
	*z = cp
	return nil
}

func (m *Memory) Rename(_ context.Context, id, name string) (*Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, ok := m.zones[id]
	if !ok {
		return nil, ErrNotFound
	}
	z.Name = name
	z.UpdatedAt = m.now()
	cp := *z
	return &cp, nil
}

func (m *Memory) SetTokenHash(_ context.Context, id, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, ok := m.zones[id]
	if !ok {
		return ErrNotFound
	}
	z.tokenHash = hash
	z.HasToken = hash != ""
	z.UpdatedAt = m.now()
	return nil
}

func (m *Memory) FindByTokenHash(_ context.Context, hash string) (*Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if hash == "" {
		return nil, ErrNotFound
	}
	for _, z := range m.zones {
		if z.tokenHash == hash {
			cp := *z
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.zones[id]; !ok {
		return ErrNotFound
	}
	delete(m.zones, id)
	return nil
}
