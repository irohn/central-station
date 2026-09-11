package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"central-station/internal/rbac"
)

// Postgres is a UserStore backed by Postgres.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres wraps a connection pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const userCols = `id, email, name, picture_url, role, google_sub, created_at, last_login_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	var role string
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PictureURL, &role, &u.GoogleSub, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = rbac.Role(role)
	return u, nil
}

func (p *Postgres) GetByID(ctx context.Context, id string) (*User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (p *Postgres) GetByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, strings.ToLower(email)))
}

// Upsert inserts a new user or refreshes profile fields of an existing one.
// The stored role is never changed here.
func (p *Postgres) Upsert(ctx context.Context, in *User) (*User, error) {
	role := in.Role
	if !role.Valid() {
		role = rbac.RoleListener
	}
	return scanUser(p.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, name, picture_url, role, google_sub)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			picture_url = EXCLUDED.picture_url,
			google_sub = CASE WHEN EXCLUDED.google_sub = '' THEN users.google_sub ELSE EXCLUDED.google_sub END,
			last_login_at = now()
		RETURNING `+userCols,
		uuid.NewString(), strings.ToLower(strings.TrimSpace(in.Email)), in.Name, in.PictureURL, string(role), in.GoogleSub))
}

func (p *Postgres) SetRole(ctx context.Context, id string, role rbac.Role) (*User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return scanUser(p.pool.QueryRow(ctx, `UPDATE users SET role = $2 WHERE id = $1 RETURNING `+userCols, id, string(role)))
}

func (p *Postgres) List(ctx context.Context) ([]*User, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
