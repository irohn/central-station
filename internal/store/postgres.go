package store

import (
	"context"
	"errors"
	"strings"
	"time"

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

const userCols = `id, email, name, picture_url, role, google_sub, created_at, last_login_at, password_hash, email_verified, verify_token, verify_expires`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	var role string
	var verifyExpires *time.Time
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PictureURL, &role, &u.GoogleSub, &u.CreatedAt, &u.LastLoginAt, &u.PasswordHash, &u.EmailVerified, &u.VerifyToken, &verifyExpires)
	if verifyExpires != nil {
		u.VerifyExpires = *verifyExpires
	}
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
		INSERT INTO users (id, email, name, picture_url, role, google_sub, email_verified)
		VALUES ($1, $2, $3, $4, $5, $6, true)
		ON CONFLICT (email) DO UPDATE SET
			name = EXCLUDED.name,
			picture_url = EXCLUDED.picture_url,
			google_sub = CASE WHEN EXCLUDED.google_sub = '' THEN users.google_sub ELSE EXCLUDED.google_sub END,
			email_verified = true,
			last_login_at = now()
		RETURNING `+userCols,
		uuid.NewString(), strings.ToLower(strings.TrimSpace(in.Email)), in.Name, in.PictureURL, string(role), in.GoogleSub))
}

func (p *Postgres) Register(ctx context.Context, in *User) (*User, error) {
	role := in.Role
	if !role.Valid() {
		role = rbac.RoleListener
	}
	u, err := scanUser(p.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, name, picture_url, role, google_sub, password_hash, email_verified)
		VALUES ($1, $2, $3, '', $4, '', $5, false)
		ON CONFLICT (email) DO NOTHING
		RETURNING `+userCols,
		uuid.NewString(), strings.ToLower(strings.TrimSpace(in.Email)), in.Name, string(role), in.PasswordHash))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrExists
	}
	return u, err
}

func (p *Postgres) SetPassword(ctx context.Context, id, hash string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	tag, err := p.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) SetVerified(ctx context.Context, id string, verified bool) (*User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return scanUser(p.pool.QueryRow(ctx, `UPDATE users SET email_verified = $2,
		verify_token = CASE WHEN $2 THEN '' ELSE verify_token END,
		verify_expires = CASE WHEN $2 THEN NULL ELSE verify_expires END
		WHERE id = $1 RETURNING `+userCols, id, verified))
}

func (p *Postgres) SetVerifyToken(ctx context.Context, id, token string, expires time.Time) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	var exp *time.Time
	if token != "" {
		exp = &expires
	}
	tag, err := p.pool.Exec(ctx, `UPDATE users SET verify_token = $2, verify_expires = $3 WHERE id = $1`, id, token, exp)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) GetByVerifyToken(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE verify_token = $1`, token))
}

func (p *Postgres) TouchLogin(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	_, err := p.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
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
