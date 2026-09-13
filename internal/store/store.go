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

	// DisplayName is a name chosen in the app; when set it is what Name
	// returns instead of the profile name Google refreshes on sign-in.
	DisplayName string `json:"displayName,omitempty"`

	// permOverride, when non-nil, replaces the role's permissions for this
	// request (set by the auth middleware from the in-memory overrides).
	permOverride []rbac.Permission

	// Password sign-in. PasswordHash is a PHC argon2id string ("" = no password).
	PasswordHash  string    `json:"-"`
	EmailVerified bool      `json:"emailVerified"`
	VerifyToken   string    `json:"-"`
	VerifyExpires time.Time `json:"-"`
}

// HasPassword reports whether the user can sign in with a password.
func (u *User) HasPassword() bool { return u.PasswordHash != "" }

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// UserStore persists users.
type UserStore interface {
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	// Upsert creates the user if the email is unknown, otherwise refreshes
	// profile fields and LastLoginAt. The stored role is kept for existing users.
	Upsert(ctx context.Context, u *User) (*User, error)
	SetRole(ctx context.Context, id string, role rbac.Role) (*User, error)
	// SetDisplayName stores the chosen name ("" = back to the profile name).
	SetDisplayName(ctx context.Context, id, name string) (*User, error)
	List(ctx context.Context) ([]*User, error)

	// Register creates a password user (unverified). ErrExists if the email is taken.
	Register(ctx context.Context, u *User) (*User, error)
	SetPassword(ctx context.Context, id, hash string) error
	SetVerified(ctx context.Context, id string, verified bool) (*User, error)
	// SetVerifyToken stores a one-time verification token; "" clears it.
	SetVerifyToken(ctx context.Context, id, token string, expires time.Time) error
	GetByVerifyToken(ctx context.Context, token string) (*User, error)
	// TouchLogin bumps LastLoginAt.
	TouchLogin(ctx context.Context, id string) error
}

// Memory is a mutex-guarded in-memory UserStore.
type Memory struct {
	mu          sync.RWMutex
	byID        map[string]*User
	byEmail     map[string]string
	profileName map[string]string // id -> name from the identity provider
	now         func() time.Time
}

// NewMemory creates an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{byID: map[string]*User{}, byEmail: map[string]string{}, profileName: map[string]string{}, now: time.Now}
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

// Upsert is the identity-provider path (Google, dev login): the address is
// verified by the provider, so it marks the user verified.
func (m *Memory) Upsert(_ context.Context, in *User) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email := strings.ToLower(strings.TrimSpace(in.Email))
	now := m.now()
	if id, ok := m.byEmail[email]; ok {
		u := m.byID[id]
		m.profileName[id] = in.Name
		u.Name = in.Name
		u.applyDisplayName()
		u.PictureURL = in.PictureURL
		if in.GoogleSub != "" {
			u.GoogleSub = in.GoogleSub
		}
		u.EmailVerified = true
		u.LastLoginAt = now
		cp := *u
		return &cp, nil
	}
	u := *in
	u.ID = uuid.NewString()
	u.Email = email
	u.CreatedAt = now
	u.LastLoginAt = now
	u.EmailVerified = true
	if !u.Role.Valid() {
		u.Role = rbac.RoleListener
	}
	m.byID[u.ID] = &u
	m.byEmail[email] = u.ID
	m.profileName[u.ID] = u.Name
	cp := u
	return &cp, nil
}

func (m *Memory) Register(_ context.Context, in *User) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if _, ok := m.byEmail[email]; ok {
		return nil, ErrExists
	}
	now := m.now()
	u := *in
	u.ID = uuid.NewString()
	u.Email = email
	u.CreatedAt = now
	u.LastLoginAt = now
	u.EmailVerified = false
	if !u.Role.Valid() {
		u.Role = rbac.RoleListener
	}
	m.byID[u.ID] = &u
	m.byEmail[email] = u.ID
	m.profileName[u.ID] = u.Name
	cp := u
	return &cp, nil
}

func (m *Memory) SetPassword(_ context.Context, id, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (m *Memory) SetVerified(_ context.Context, id string, verified bool) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	u.EmailVerified = verified
	if verified {
		u.VerifyToken, u.VerifyExpires = "", time.Time{}
	}
	cp := *u
	return &cp, nil
}

func (m *Memory) SetVerifyToken(_ context.Context, id, token string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	u.VerifyToken, u.VerifyExpires = token, expires
	return nil
}

func (m *Memory) GetByVerifyToken(_ context.Context, token string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if token == "" {
		return nil, ErrNotFound
	}
	for _, u := range m.byID {
		if u.VerifyToken == token {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) TouchLogin(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	u.LastLoginAt = m.now()
	return nil
}

// SetPermissionOverride replaces the role's permissions for this request.
func (u *User) SetPermissionOverride(p []rbac.Permission) { u.permOverride = p }

// PermissionsOverridden reports whether an override is in effect.
func (u *User) PermissionsOverridden() bool { return u.permOverride != nil }

// Permissions returns the effective permissions: the override if one is set,
// else the role's.
func (u *User) Permissions() []rbac.Permission {
	if u.permOverride != nil {
		return append([]rbac.Permission(nil), u.permOverride...)
	}
	return u.Role.Permissions()
}

// Has reports whether the user holds the permission.
func (u *User) Has(p rbac.Permission) bool {
	for _, got := range u.Permissions() {
		if got == p {
			return true
		}
	}
	return false
}

// applyDisplayName makes Name the display name when one is set.
func (u *User) applyDisplayName() {
	if u.DisplayName != "" {
		u.Name = u.DisplayName
	}
}

func (m *Memory) SetDisplayName(_ context.Context, id, name string) (*User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	u.DisplayName = strings.TrimSpace(name)
	if u.DisplayName == "" {
		u.Name = m.profileName[id]
	} else {
		u.Name = u.DisplayName
	}
	cp := *u
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
