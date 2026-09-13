package v1

import "testing"

func TestPlayNextAndPlayNow(t *testing.T) {
	a := newTestAPI(t)
	mgr, _ := a.login("mgr@example.com", "manager")
	col, _ := a.login("col@example.com", "collaborator")
	ids := func() (cur string, items []string) {
		q := a.do("GET", "/queue", mgr, nil)
		if c, ok := q.Body["current"].(map[string]any); ok {
			cur = c["track"].(map[string]any)["title"].(string)
		}
		for _, it := range q.Body["items"].([]any) {
			items = append(items, it.(map[string]any)["track"].(map[string]any)["title"].(string))
		}
		return
	}
	for _, s := range []string{"a", "b", "c"} {
		a.do("POST", "/queue", mgr, map[string]string{"input": "song " + s})
	}
	if cur, items := ids(); cur != "result for song a" || len(items) != 2 {
		t.Fatalf("setup: %q %v", cur, items)
	}
	// Collaborators may add but not jump the queue.
	if r := a.do("POST", "/queue", col, map[string]any{"input": "song x", "playNext": true}); r.Status != 403 {
		t.Fatalf("collab playNext: %d %v", r.Status, r.Body)
	}
	if r := a.do("POST", "/queue", col, map[string]any{"input": "song x", "playNow": true}); r.Status != 403 {
		t.Fatalf("collab playNow: %d %v", r.Status, r.Body)
	}
	// Play next: first in line, current untouched.
	if r := a.do("POST", "/queue", mgr, map[string]any{"input": "song n", "playNext": true}); r.Status != 201 {
		t.Fatalf("playNext: %d %v", r.Status, r.Body)
	}
	if cur, items := ids(); cur != "result for song a" || items[0] != "result for song n" || len(items) != 3 {
		t.Fatalf("after playNext: %q %v", cur, items)
	}
	// Play now: becomes current, the previous current is skipped into history.
	if r := a.do("POST", "/queue", mgr, map[string]any{"input": "song w", "playNow": true}); r.Status != 201 {
		t.Fatalf("playNow: %d %v", r.Status, r.Body)
	}
	if cur, items := ids(); cur != "result for song w" || items[0] != "result for song n" || len(items) != 3 {
		t.Fatalf("after playNow: %q %v", cur, items)
	}
	h := a.do("GET", "/history", mgr, nil)
	if e := h.Body["entries"].([]any); len(e) != 1 || e[0].(map[string]any)["endReason"] != "skipped" {
		t.Fatalf("history: %v", h.Body)
	}
}
