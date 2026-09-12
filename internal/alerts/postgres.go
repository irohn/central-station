package alerts

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres stores alerts in the alerts table.
type Postgres struct{ pool *pgxpool.Pool }

// NewPostgres wraps a connection pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

const cols = `id, name, enabled, type, schedule, timezone, clip_id, loops, chime_id, created_by_id, created_by_name, created_at, updated_at, last_fired_at, last_error, zone_id`

func scan(row pgx.Row) (*Alert, error) {
	a := &Alert{}
	var fired *time.Time
	err := row.Scan(&a.ID, &a.Name, &a.Enabled, &a.Type, &a.Schedule, &a.Timezone, &a.ClipID, &a.Loops, &a.ChimeID,
		&a.CreatedByID, &a.CreatedByName, &a.CreatedAt, &a.UpdatedAt, &fired, &a.LastError, &a.ZoneID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.LastFiredAt = fired
	return a, nil
}

func (p *Postgres) List(ctx context.Context) ([]*Alert, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+cols+` FROM alerts ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Alert
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) Get(ctx context.Context, id string) (*Alert, error) {
	return scan(p.pool.QueryRow(ctx, `SELECT `+cols+` FROM alerts WHERE id = $1`, id))
}

func (p *Postgres) Insert(ctx context.Context, a *Alert) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO alerts (`+cols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		a.ID, a.Name, a.Enabled, a.Type, a.Schedule, a.Timezone, a.ClipID, a.Loops, a.ChimeID,
		a.CreatedByID, a.CreatedByName, a.CreatedAt, a.UpdatedAt, a.LastFiredAt, a.LastError, a.ZoneID)
	return err
}

func (p *Postgres) Update(ctx context.Context, a *Alert) error {
	tag, err := p.pool.Exec(ctx, `UPDATE alerts SET name=$2, enabled=$3, type=$4, schedule=$5, timezone=$6, clip_id=$7, loops=$8, chime_id=$9, updated_at=$10, zone_id=$11
		WHERE id = $1`, a.ID, a.Name, a.Enabled, a.Type, a.Schedule, a.Timezone, a.ClipID, a.Loops, a.ChimeID, a.UpdatedAt, a.ZoneID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) Delete(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM alerts WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) MarkFired(ctx context.Context, id string, at time.Time, errMsg string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE alerts SET last_fired_at = $2, last_error = $3 WHERE id = $1`, id, at, errMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
