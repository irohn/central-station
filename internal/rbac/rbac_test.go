package rbac

import "testing"

func TestRoleHierarchy(t *testing.T) {
	checks := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleListener, QueueRead, true},
		{RoleListener, HistoryRead, true},
		{RoleListener, QueueAdd, false},
		{RoleListener, QueueRemoveOwn, false},
		{RoleCollaborator, QueueAdd, true},
		{RoleCollaborator, QueueRemoveOwn, true},
		{RoleCollaborator, QueueRemoveAny, false},
		{RoleCollaborator, PlaybackControl, false},
		{RoleCollaborator, VolumeControl, true},
		{RoleListener, VolumeControl, false},
		{RoleManager, QueueRemoveAny, true},
		{RoleManager, QueueReorder, true},
		{RoleManager, PlaybackControl, true},
		{RoleManager, PlaylistRemoveAny, true},
		{RoleCollaborator, PlaylistRemoveAny, false},
		{RoleCollaborator, PlaylistWrite, true},
		{RoleManager, UsersManage, false},
		{RoleAdmin, UsersManage, true},
		{RoleAdmin, IntegrationsManage, true},
		{RoleAdmin, QueueAdd, true},
		{Role("bogus"), QueueRead, false},
	}
	for _, c := range checks {
		if got := c.role.Has(c.perm); got != c.want {
			t.Errorf("%s.Has(%s) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestConfigure(t *testing.T) {
	t.Cleanup(ResetGrants)
	// Move volume control up to managers and drop playlist:write entirely.
	err := Configure(map[Role][]Permission{
		RoleCollaborator: {QueueAdd, QueueRemoveOwn},
		RoleManager:      {QueueRemoveAny, QueueReorder, PlaybackControl, FallbackManage, "Volume:Control"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if RoleCollaborator.Has(VolumeControl) || !RoleManager.Has(VolumeControl) || RoleManager.Has(PlaylistWrite) {
		t.Fatalf("override not applied: collab=%v manager=%v", RoleCollaborator.Permissions(), RoleManager.Permissions())
	}
	if !RoleListener.Has(QueueRead) {
		t.Fatal("untouched level lost its defaults")
	}
	// Admins keep everything even when a lower level dropped a permission.
	if !RoleAdmin.Has(PlaylistWrite) || !RoleAdmin.Has(UsersManage) {
		t.Fatal("admin must always hold every permission")
	}
	if err := Configure(map[Role][]Permission{RoleManager: {"queue:fly"}}); err == nil {
		t.Fatal("unknown permission accepted")
	}
	if err := Configure(map[Role][]Permission{"owner": {QueueRead}}); err == nil {
		t.Fatal("unknown role accepted")
	}
	// A rejected configuration must leave the previous policy in place.
	if RoleCollaborator.Has(VolumeControl) {
		t.Fatal("failed Configure changed the policy")
	}
}

func TestParseRole(t *testing.T) {
	if r, err := ParseRole(" Manager "); err != nil || r != RoleManager {
		t.Fatalf("got %q, %v", r, err)
	}
	if _, err := ParseRole("owner"); err == nil {
		t.Fatal("expected error")
	}
	if !RoleAdmin.AtLeast(RoleManager) || RoleListener.AtLeast(RoleCollaborator) {
		t.Fatal("AtLeast ordering wrong")
	}
}
