// Package rbac defines roles and the permissions each role grants.
//
// Roles are cumulative: every role includes the permissions of the roles below it.
//
//	listener     < collaborator < manager < admin
package rbac

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Role is a named bundle of permissions assigned to a user.
type Role string

const (
	RoleListener     Role = "listener"
	RoleCollaborator Role = "collaborator"
	RoleManager      Role = "manager"
	RoleAdmin        Role = "admin"
)

// Permission is a fine-grained capability checked by handlers.
type Permission string

const (
	// Read-only.
	QueueRead    Permission = "queue:read"
	HistoryRead  Permission = "history:read"
	TrackResolve Permission = "track:resolve" // look up a URL / search without queueing it

	// Collaborator.
	QueueAdd       Permission = "queue:add"
	QueueRemoveOwn Permission = "queue:remove_own"
	PlaylistWrite  Permission = "playlist:write" // queue whole playlists; manage your own
	VolumeControl  Permission = "volume:control" // set the output level

	// Manager.
	QueueRemoveAny    Permission = "queue:remove_any"
	PlaylistRemoveAny Permission = "playlist:remove_any" // skip or reshuffle anyone's queued playlist
	BroadcastSend     Permission = "broadcast:send"      // record/synthesise clips and announce them over the music
	AlertsManage      Permission = "alerts:manage"       // schedule announcements
	QueueReorder      Permission = "queue:reorder"
	PlaybackControl   Permission = "playback:control" // skip, pause, resume, override
	OutputControl     Permission = "output:control"   // choose which speakers a zone's player uses
	FallbackManage    Permission = "fallback:manage"  // configure the default playlist

	// Admin.
	SettingsManage     Permission = "settings:manage"
	UsersManage        Permission = "users:manage"
	IntegrationsManage Permission = "integrations:manage" // speakers, Spotify/Apple Music links
)

// roleLevel orders roles; a higher level inherits everything below it.
var roleLevel = map[Role]int{
	RoleListener:     0,
	RoleCollaborator: 1,
	RoleManager:      2,
	RoleAdmin:        3,
}

// AllPermissions lists every permission the API knows about.
var AllPermissions = []Permission{
	QueueRead, HistoryRead, TrackResolve,
	QueueAdd, QueueRemoveOwn, PlaylistWrite, VolumeControl,
	QueueRemoveAny, QueueReorder, PlaybackControl, OutputControl, FallbackManage, PlaylistRemoveAny, BroadcastSend, AlertsManage,
	SettingsManage, UsersManage, IntegrationsManage,
}

// DefaultGrants lists the permissions introduced at each level.
var DefaultGrants = map[Role][]Permission{
	RoleListener:     {QueueRead, HistoryRead, TrackResolve},
	RoleCollaborator: {QueueAdd, QueueRemoveOwn, PlaylistWrite, VolumeControl},
	RoleManager:      {QueueRemoveAny, QueueReorder, PlaybackControl, OutputControl, FallbackManage, PlaylistRemoveAny, BroadcastSend, AlertsManage},
	RoleAdmin:        {SettingsManage, UsersManage, IntegrationsManage},
}

// grants is the active policy; see Configure.
var (
	grantsMu sync.RWMutex
	grants   = copyGrants(DefaultGrants)
)

func copyGrants(g map[Role][]Permission) map[Role][]Permission {
	out := make(map[Role][]Permission, len(g))
	for r, ps := range g {
		out[r] = append([]Permission(nil), ps...)
	}
	return out
}

// Configure replaces the permissions introduced at the given levels (the
// defaults stay for roles that are not listed). Roles remain cumulative, and
// admins always hold every permission so a misconfiguration can never lock
// the administrators out. Unknown roles or permissions are rejected.
func Configure(overrides map[Role][]Permission) error {
	next := copyGrants(DefaultGrants)
	for role, perms := range overrides {
		if !role.Valid() {
			return fmt.Errorf("unknown role %q", role)
		}
		clean := make([]Permission, 0, len(perms))
		for _, p := range perms {
			parsed, err := ParsePermission(string(p))
			if err != nil {
				return fmt.Errorf("role %s: %w", role, err)
			}
			clean = append(clean, parsed)
		}
		next[role] = clean
	}
	grantsMu.Lock()
	grants = next
	grantsMu.Unlock()
	return nil
}

// ResetGrants restores the default policy (tests).
func ResetGrants() {
	grantsMu.Lock()
	grants = copyGrants(DefaultGrants)
	grantsMu.Unlock()
}

// Grants returns the permissions introduced at each level under the active policy.
func Grants() map[Role][]Permission {
	grantsMu.RLock()
	defer grantsMu.RUnlock()
	return copyGrants(grants)
}

// ParsePermission validates a permission name (case-insensitive).
func ParsePermission(s string) (Permission, error) {
	p := Permission(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range AllPermissions {
		if known == p {
			return p, nil
		}
	}
	names := make([]string, len(AllPermissions))
	for i, k := range AllPermissions {
		names[i] = string(k)
	}
	sort.Strings(names)
	return "", fmt.Errorf("unknown permission %q (valid: %s)", s, strings.Join(names, ", "))
}

// AllRoles lists roles from least to most privileged.
var AllRoles = []Role{RoleListener, RoleCollaborator, RoleManager, RoleAdmin}

// ParseRole validates a role name (case-insensitive).
func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	if _, ok := roleLevel[r]; !ok {
		names := make([]string, len(AllRoles))
		for i, r := range AllRoles {
			names[i] = string(r)
		}
		return "", fmt.Errorf("unknown role %q (valid: %s)", s, strings.Join(names, ", "))
	}
	return r, nil
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := roleLevel[r]; return ok }

// Level returns the privilege level of the role (0 = lowest).
func (r Role) Level() int { return roleLevel[r] }

// AtLeast reports whether r is the same as or more privileged than other.
func (r Role) AtLeast(other Role) bool { return r.Valid() && roleLevel[r] >= roleLevel[other] }

// Permissions returns every permission the role grants (deduplicated, in
// definition order). Admins always get everything.
func (r Role) Permissions() []Permission {
	if !r.Valid() {
		return nil
	}
	if r == RoleAdmin {
		return append([]Permission(nil), AllPermissions...)
	}
	grantsMu.RLock()
	defer grantsMu.RUnlock()
	seen := map[Permission]bool{}
	var out []Permission
	for _, role := range AllRoles {
		if roleLevel[role] <= roleLevel[r] {
			for _, p := range grants[role] {
				if !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// Has reports whether the role grants the permission.
func (r Role) Has(p Permission) bool {
	for _, got := range r.Permissions() {
		if got == p {
			return true
		}
	}
	return false
}
