package clips

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres stores clips (audio included) in the clips table.
type Postgres struct{ pool *pgxpool.Pool }

// NewPostgres wraps a connection pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const cols = `id, name, kind, mime, size, duration_seconds, text, voice, created_by_id, created_by_name, created_at, builtin`

func scan(row pgx.Row) (*Clip, error) {
	c := &Clip{}
	var kind string
	err := row.Scan(&c.ID, &c.Name, &kind, &c.Mime, &c.Size, &c.DurationSeconds, &c.Text, &c.Voice, &c.CreatedByID, &c.CreatedByName, &c.CreatedAt, &c.Builtin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Kind = Kind(kind)
	return c, nil
}

func (p *Postgres) List(ctx context.Context) ([]*Clip, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+cols+` FROM clips ORDER BY builtin DESC, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Clip
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) Get(ctx context.Context, id string) (*Clip, error) {
	return scan(p.pool.QueryRow(ctx, `SELECT `+cols+` FROM clips WHERE id = $1`, id))
}

func (p *Postgres) Audio(ctx context.Context, id string) (*Clip, []byte, error) {
	c := &Clip{}
	var kind string
	var audio []byte
	err := p.pool.QueryRow(ctx, `SELECT `+cols+`, audio FROM clips WHERE id = $1`, id).Scan(
		&c.ID, &c.Name, &kind, &c.Mime, &c.Size, &c.DurationSeconds, &c.Text, &c.Voice, &c.CreatedByID, &c.CreatedByName, &c.CreatedAt, &c.Builtin, &audio)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	c.Kind = Kind(kind)
	return c, audio, nil
}

func (p *Postgres) Insert(ctx context.Context, c *Clip, audio []byte) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO clips (`+cols+`, audio) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		c.ID, c.Name, string(c.Kind), c.Mime, c.Size, c.DurationSeconds, c.Text, c.Voice, c.CreatedByID, c.CreatedByName, c.CreatedAt, c.Builtin, audio)
	return err
}

func (p *Postgres) Rename(ctx context.Context, id, name string) (*Clip, error) {
	return scan(p.pool.QueryRow(ctx, `UPDATE clips SET name = $2 WHERE id = $1 RETURNING `+cols, id, name))
}

func (p *Postgres) Delete(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM clips WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
