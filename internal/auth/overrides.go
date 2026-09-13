package auth

import (
	"sync"

	"central-station/internal/rbac"
)

// Overrides holds per-user permission sets that replace the role's
// permissions for this API process only (they are not persisted; a restart
// returns everyone to their role).
type Overrides struct {
	mu sync.RWMutex
	m  map[string][]rbac.Permission
}

// NewOverrides creates an empty set.
func NewOverrides() *Overrides { return &Overrides{m: map[string][]rbac.Permission{}} }

// Get returns the override for a user, if any.
func (o *Overrides) Get(userID string) ([]rbac.Permission, bool) {
	if o == nil {
		return nil, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	p, ok := o.m[userID]
	if !ok {
		return nil, false
	}
	return append([]rbac.Permission(nil), p...), true
}

// Set replaces a user's permissions (an empty list means "no permissions").
func (o *Overrides) Set(userID string, perms []rbac.Permission) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.m[userID] = append([]rbac.Permission{}, perms...)
}

// Clear returns the user to their role's permissions.
func (o *Overrides) Clear(userID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.m, userID)
}

// Apply stamps the override (if any) onto u.
func (o *Overrides) Apply(u interface{ SetPermissionOverride([]rbac.Permission) }, userID string) {
	if p, ok := o.Get(userID); ok {
		u.SetPermissionOverride(p)
	}
}
