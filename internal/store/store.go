// Package store defines persistence interfaces and an in-memory implementation.
// Swap Memory for a database-backed implementation without touching handlers.
package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"central-station/internal/rbac"
)

// User is an authenticated account.
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	PictureURL  string    `json:"pictureUrl,omitempty"`
	Role        rbac.Role `json:"role"`
	GoogleSub   string    `json:"-"`
	CreatedAt   time.Time `json:"createdAt"`
	LastLoginAt time.Time `json:"lastLoginAt"`
}

var ErrNotFound = errors.New("not found")

// UserStore persists users.
type UserStore interface {
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	// Upsert creates the user if the email is unknown, otherwise refreshes
	// profile fields and LastLoginAt. The stored role is kept for existing users.
	Upsert(ctx context.Context, u *User) (*User, error)
	SetRole(ctx context.Context, id string, role rbac.Role) (*User, error)
	List(ctx context.Context) ([]*User, error)
}

// Memory is a mutex-guarded in-memory UserStore.
type Memory struct {
	mu      sync.RWMutex
	byID    map[string]*User
	byEmail map[string]string
	now     func() time.Time
}

// NewMemory creates an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{byID: map[string]*User{}, byEmail: map[string]string{}, now: time.Now}
}

func (m *Memory) GetByID(_ context.Context, id string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *Memory) GetByEmail(_ context.Context, email string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byEmail[strings.ToLower(email)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *m.byID[id]
	return &cp, nil
}

func (m *Memory) Upsert(_ context.Context, in *User) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email := strings.ToLower(strings.TrimSpace(in.Email))
	now := m.now()
	if id, ok := m.byEmail[email]; ok {
		u := m.byID[id]
		u.Name = in.Name
		u.PictureURL = in.PictureURL
		if in.GoogleSub != "" {
			u.GoogleSub = in.GoogleSub
		}
		u.LastLoginAt = now
		cp := *u
		return &cp, nil
	}
	u := *in
	u.ID = uuid.NewString()
	u.Email = email
	u.CreatedAt = now
	u.LastLoginAt = now
	if !u.Role.Valid() {
		u.Role = rbac.RoleListener
	}
	m.byID[u.ID] = &u
	m.byEmail[email] = u.ID
	cp := u
	return &cp, nil
}

func (m *Memory) SetRole(_ context.Context, id string, role rbac.Role) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	u.Role = role
	cp := *u
	return &cp, nil
}

func (m *Memory) List(_ context.Context) ([]*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*User, 0, len(m.byID))
	for _, u := range m.byID {
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
