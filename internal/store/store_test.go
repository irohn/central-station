package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"central-station/internal/db"
	"central-station/internal/rbac"
)

func userStoreConformance(t *testing.T, s UserStore) {
	t.Helper()
	ctx := context.Background()

	if _, err := s.GetByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing email: %v", err)
	}
	if _, err := s.GetByID(ctx, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}

	u, err := s.Upsert(ctx, &User{Email: " Ann@Example.com ", Name: "Ann", PictureURL: "p1", GoogleSub: "g1", Role: rbac.RoleManager})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Email != "ann@example.com" || u.Role != rbac.RoleManager || u.CreatedAt.IsZero() {
		t.Fatalf("created user: %+v", u)
	}

	// Existing user: profile refreshed, role kept, empty google sub does not clobber.
	u2, err := s.Upsert(ctx, &User{Email: "ann@example.com", Name: "Ann B", PictureURL: "p2", Role: rbac.RoleListener})
	if err != nil {
		t.Fatal(err)
	}
	if u2.ID != u.ID || u2.Name != "Ann B" || u2.PictureURL != "p2" || u2.Role != rbac.RoleManager || u2.GoogleSub != "g1" {
		t.Fatalf("upserted user: %+v", u2)
	}
	if !u2.LastLoginAt.After(u.CreatedAt) && !u2.LastLoginAt.Equal(u.CreatedAt) {
		t.Fatalf("last login not refreshed: %s vs %s", u2.LastLoginAt, u.CreatedAt)
	}

	if _, err := s.Upsert(ctx, &User{Email: "bob@example.com", Name: "Bob", Role: rbac.Role("bogus")}); err != nil {
		t.Fatal(err)
	}
	bob, _ := s.GetByEmail(ctx, "bob@example.com")
	if bob.Role != rbac.RoleListener {
		t.Fatalf("invalid role should default to listener: %s", bob.Role)
	}

	got, err := s.SetRole(ctx, bob.ID, rbac.RoleAdmin)
	if err != nil || got.Role != rbac.RoleAdmin {
		t.Fatalf("set role: %+v %v", got, err)
	}
	if _, err := s.SetRole(ctx, "00000000-0000-4000-8000-000000000000", rbac.RoleAdmin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set role missing: %v", err)
	}
	byID, err := s.GetByID(ctx, bob.ID)
	if err != nil || byID.Role != rbac.RoleAdmin {
		t.Fatalf("get by id: %+v %v", byID, err)
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].Email != "ann@example.com" {
		t.Fatalf("list: %v %v", list, err)
	}
}

func TestMemoryUserStore(t *testing.T) { userStoreConformance(t, NewMemory()) }

func TestPostgresUserStore(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users`); err != nil {
		t.Fatal(err)
	}
	userStoreConformance(t, NewPostgres(pool))
}
