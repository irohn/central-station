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
		{RoleManager, QueueRemoveAny, true},
		{RoleManager, QueueReorder, true},
		{RoleManager, PlaybackControl, true},
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
