package zone

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is a Store backed by the zones table.
type Postgres struct{ pool *pgxpool.Pool }

// NewPostgres wraps a connection pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const cols = `id, name, token_hash, position, created_at, updated_at`

func scan(row pgx.Row) (*Zone, error) {
	z := &Zone{}
	err := row.Scan(&z.ID, &z.Name, &z.tokenHash, &z.Position, &z.CreatedAt, &z.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	z.HasToken = z.tokenHash != ""
	return z, nil
}

func (p *Postgres) List(ctx context.Context) ([]*Zone, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+cols+` FROM zones ORDER BY position, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Zone
	for rows.Next() {
		z, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func (p *Postgres) Get(ctx context.Context, id string) (*Zone, error) {
	return scan(p.pool.QueryRow(ctx, `SELECT `+cols+` FROM zones WHERE id = $1`, id))
}

func (p *Postgres) Create(ctx context.Context, z *Zone, tokenHash string) error {
	got, err := scan(p.pool.QueryRow(ctx, `INSERT INTO zones (id, name, token_hash, position)
		VALUES ($1, $2, $3, $4) RETURNING `+cols, z.ID, z.Name, tokenHash, z.Position))
	if err != nil {
		return err
	}
	*z = *got
	return nil
}

func (p *Postgres) Rename(ctx context.Context, id, name string) (*Zone, error) {
	return scan(p.pool.QueryRow(ctx, `UPDATE zones SET name = $2, updated_at = now() WHERE id = $1 RETURNING `+cols, id, name))
}

func (p *Postgres) SetTokenHash(ctx context.Context, id, hash string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE zones SET token_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) FindByTokenHash(ctx context.Context, hash string) (*Zone, error) {
	if hash == "" {
		return nil, ErrNotFound
	}
	return scan(p.pool.QueryRow(ctx, `SELECT `+cols+` FROM zones WHERE token_hash = $1`, hash))
}

// Delete removes the zone; queue, playlists, history and playback rows go with
// it through foreign keys, and its settings keys are removed here.
func (p *Postgres) Delete(ctx context.Context, id string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM zones WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM settings WHERE key LIKE $1`, SettingsPrefix(id)+"%")
		return err
	})
}
