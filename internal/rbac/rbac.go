// Package rbac defines roles and the permissions each role grants.
//
// Roles are cumulative: every role includes the permissions of the roles below it.
//
//	listener     < collaborator < manager < admin
package rbac

import (
	"fmt"
	"strings"
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
	PlaylistWrite  Permission = "playlist:write" // collaborative office playlists

	// Manager.
	QueueRemoveAny  Permission = "queue:remove_any"
	QueueReorder    Permission = "queue:reorder"
	PlaybackControl Permission = "playback:control" // skip, pause, resume, override
	FallbackManage  Permission = "fallback:manage"  // configure the default playlist

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

// grants lists the permissions introduced at each level.
var grants = map[Role][]Permission{
	RoleListener:     {QueueRead, HistoryRead, TrackResolve},
	RoleCollaborator: {QueueAdd, QueueRemoveOwn, PlaylistWrite},
	RoleManager:      {QueueRemoveAny, QueueReorder, PlaybackControl, FallbackManage},
	RoleAdmin:        {SettingsManage, UsersManage, IntegrationsManage},
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

// Permissions returns every permission the role grants.
func (r Role) Permissions() []Permission {
	if !r.Valid() {
		return nil
	}
	var out []Permission
	for _, role := range AllRoles {
		if roleLevel[role] <= roleLevel[r] {
			out = append(out, grants[role]...)
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
