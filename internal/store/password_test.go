package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"central-station/internal/rbac"
)

func passwordConformance(t *testing.T, s UserStore) {
	t.Helper()
	ctx := context.Background()
	u, err := s.Register(ctx, &User{Email: " Pat@Example.com ", Name: "Pat", PasswordHash: "$argon2id$hash", Role: rbac.RoleListener})
	if err != nil {
		t.Fatal(err)
	}
	if u.EmailVerified || !u.HasPassword() || u.Email != "pat@example.com" || u.Role != rbac.RoleListener {
		t.Fatalf("registered: %+v", u)
	}
	if _, err := s.Register(ctx, &User{Email: "pat@example.com", Name: "Again", PasswordHash: "x"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	got, _ := s.GetByEmail(ctx, "pat@example.com")
	if got.PasswordHash != "$argon2id$hash" {
		t.Fatalf("hash not loaded: %+v", got)
	}
	if err := s.SetVerifyToken(ctx, u.ID, "tok123", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	byTok, err := s.GetByVerifyToken(ctx, "tok123")
	if err != nil || byTok.ID != u.ID || byTok.VerifyExpires.IsZero() {
		t.Fatalf("by token: %v %+v", err, byTok)
	}
	if _, err := s.GetByVerifyToken(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("empty token matched")
	}
	v, err := s.SetVerified(ctx, u.ID, true)
	if err != nil || !v.EmailVerified || v.VerifyToken != "" {
		t.Fatalf("verified: %v %+v", err, v)
	}
	if _, err := s.GetByVerifyToken(ctx, "tok123"); !errors.Is(err, ErrNotFound) {
		t.Fatal("token survived verification")
	}
	if err := s.SetPassword(ctx, u.ID, "$argon2id$new"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetByID(ctx, u.ID); got.PasswordHash != "$argon2id$new" {
		t.Fatalf("password not updated")
	}
	if err := s.TouchLogin(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	// The provider path marks the same address verified without touching the password.
	up, err := s.Upsert(ctx, &User{Email: "pat@example.com", Name: "Pat G", GoogleSub: "g9"})
	if err != nil || !up.EmailVerified || up.PasswordHash != "$argon2id$new" || up.GoogleSub != "g9" {
		t.Fatalf("upsert over password user: %v %+v", err, up)
	}
	if _, err := s.SetVerified(ctx, "00000000-0000-4000-8000-000000000000", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}
