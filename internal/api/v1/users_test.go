package v1

import "testing"

func TestDisplayNames(t *testing.T) {
	a := newTestAPI(t)
	admin, _ := a.login("root@example.com", "")
	col, colID := a.login("col@example.com", "collaborator")
	lis, lisID := a.login("lis@example.com", "listener")

	// Anyone can rename themselves; the new name shows in /me and the list.
	if r := a.do("PATCH", "/users/"+colID, col, map[string]string{"name": "  DJ Col  "}); r.Status != 200 || r.Body["name"] != "DJ Col" {
		t.Fatalf("self rename: %d %v", r.Status, r.Body)
	}
	if r := a.do("GET", "/me", col, nil); r.Body["name"] != "DJ Col" {
		t.Fatalf("me after rename: %v", r.Body)
	}
	// ...but not someone else.
	if r := a.do("PATCH", "/users/"+lisID, col, map[string]string{"name": "x"}); r.Status != 403 {
		t.Fatalf("rename other: %d", r.Status)
	}
	// Admins rename anyone; too long is rejected.
	if r := a.do("PATCH", "/users/"+lisID, admin, map[string]string{"name": "Listener Larry"}); r.Status != 200 || r.Body["name"] != "Listener Larry" {
		t.Fatalf("admin rename: %d %v", r.Status, r.Body)
	}
	if r := a.do("PATCH", "/users/"+lisID, admin, map[string]string{"name": string(make([]byte, 61))}); r.Status != 400 {
		t.Fatalf("long name: %d", r.Status)
	}
	// A fresh sign-in (Google refreshes the profile name) keeps the display name.
	lis2, _ := a.login("lis@example.com", "listener")
	if r := a.do("GET", "/me", lis2, nil); r.Body["name"] != "Listener Larry" {
		t.Fatalf("display name lost on re-login: %v", r.Body["name"])
	}
	// Empty restores the profile name.
	if r := a.do("PATCH", "/users/"+lisID, lis, map[string]string{"name": ""}); r.Status != 200 || r.Body["name"] != "lis" {
		t.Fatalf("clear name: %d %v", r.Status, r.Body)
	}
}

func TestPermissionOverrides(t *testing.T) {
	a := newTestAPI(t)
	admin, adminID := a.login("root@example.com", "")
	mgr, _ := a.login("mgr@example.com", "manager")
	lis, lisID := a.login("lis@example.com", "listener")

	// Managers cannot override; admins cannot override themselves.
	if r := a.do("PUT", "/users/"+lisID+"/permissions", mgr, map[string]any{"permissions": []string{"queue:add"}}); r.Status != 403 {
		t.Fatalf("manager override: %d", r.Status)
	}
	if r := a.do("PUT", "/users/"+adminID+"/permissions", admin, map[string]any{"permissions": []string{"queue:add"}}); r.Status != 409 {
		t.Fatalf("self override: %d %v", r.Status, r.Body)
	}
	if r := a.do("PUT", "/users/"+lisID+"/permissions", admin, map[string]any{"permissions": []string{"nope:x"}}); r.Status != 400 || errCode(r) != "invalid_permission" {
		t.Fatalf("bad permission: %d %v", r.Status, r.Body)
	}

	// A listener cannot add; grant queue:add + playback:control by override.
	if r := a.do("POST", "/queue", lis, map[string]string{"input": "song"}); r.Status != 403 {
		t.Fatalf("listener add before: %d", r.Status)
	}
	r := a.do("PUT", "/users/"+lisID+"/permissions", admin, map[string]any{"permissions": []string{"queue:read", "queue:add", "playback:control"}})
	if r.Status != 200 || r.Body["permissionsOverridden"] != true || len(r.Body["permissions"].([]any)) != 3 {
		t.Fatalf("override: %d %v", r.Status, r.Body)
	}
	// Effective immediately, with the existing session; role unchanged.
	if r := a.do("GET", "/me", lis, nil); r.Body["role"] != "listener" || r.Body["permissionsOverridden"] != true || len(r.Body["permissions"].([]any)) != 3 {
		t.Fatalf("me with override: %v", r.Body)
	}
	if r := a.do("POST", "/queue", lis, map[string]string{"input": "song"}); r.Status != 201 {
		t.Fatalf("listener add with override: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/playback/pause", lis, nil); r.Status != 200 {
		t.Fatalf("listener pause with override: %d %v", r.Status, r.Body)
	}
	// Taking history:read away works too (override replaces the role's set).
	if r := a.do("GET", "/history", lis, nil); r.Status != 403 {
		t.Fatalf("history removed by override: %d", r.Status)
	}
	// The list reflects the override; reset returns to the role.
	if r := a.do("GET", "/users", admin, nil); r.Status != 200 {
		t.Fatalf("list: %d", r.Status)
	} else {
		found := false
		for _, u := range r.Body["users"].([]any) {
			m := u.(map[string]any)
			if m["id"] == lisID && m["permissionsOverridden"] == true {
				found = true
			}
		}
		if !found {
			t.Fatalf("override not reflected in list: %v", r.Body)
		}
	}
	if r := a.do("DELETE", "/users/"+lisID+"/permissions", admin, nil); r.Status != 200 || r.Body["permissionsOverridden"] != false {
		t.Fatalf("reset: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/queue", lis, map[string]string{"input": "song"}); r.Status != 403 {
		t.Fatalf("listener add after reset: %d", r.Status)
	}
	if r := a.do("GET", "/history", lis, nil); r.Status != 200 {
		t.Fatalf("history back after reset: %d", r.Status)
	}
}
