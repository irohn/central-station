package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore persists the queue in Postgres.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore wraps a connection pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (p *PostgresStore) Load(ctx context.Context) (*Persisted, error) {
	out := &Persisted{Status: StatusIdle}
	rows, err := p.pool.Query(ctx, `SELECT id, track, added_by_id, added_by_name, added_at FROM queue_items ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it := &Item{}
		var track []byte
		if err := rows.Scan(&it.ID, &track, &it.AddedByID, &it.AddedByName, &it.AddedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(track, &it.Track); err != nil {
			return nil, fmt.Errorf("decode track for %s: %w", it.ID, err)
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var status string
	var current []byte
	var startedAt *time.Time
	err = p.pool.QueryRow(ctx, `SELECT status, current, started_at FROM playback WHERE id`).Scan(&status, &current, &startedAt)
	switch {
	case err == pgx.ErrNoRows:
		return out, nil
	case err != nil:
		return nil, err
	}
	out.Status = Status(status)
	if current != nil {
		out.Current = &Item{}
		if err := json.Unmarshal(current, out.Current); err != nil {
			return nil, fmt.Errorf("decode current item: %w", err)
		}
	}
	if startedAt != nil {
		out.StartedAt = *startedAt
	}
	if out.Current == nil {
		out.Status = StatusIdle
	}
	return out, nil
}

func (p *PostgresStore) Insert(ctx context.Context, it *Item) error {
	track, err := json.Marshal(it.Track)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO queue_items (id, position, track, added_by_id, added_by_name, added_at)
		VALUES ($1, (SELECT COALESCE(MAX(position), 0) + 1 FROM queue_items), $2, $3, $4, $5)`,
		it.ID, track, it.AddedByID, it.AddedByName, it.AddedAt)
	return err
}

func (p *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM queue_items WHERE id = $1`, id)
	return err
}

func (p *PostgresStore) Reorder(ctx context.Context, ids []string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		for i, id := range ids {
			if _, err := tx.Exec(ctx, `UPDATE queue_items SET position = $1 WHERE id = $2`, i+1, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *PostgresStore) SetPlayback(ctx context.Context, status Status, current *Item, startedAt time.Time) error {
	return setPlayback(ctx, p.pool, status, current, startedAt)
}

// execer is satisfied by both *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func setPlayback(ctx context.Context, db execer, status Status, current *Item, startedAt time.Time) error {
	var cur []byte
	if current != nil {
		b, err := json.Marshal(current)
		if err != nil {
			return err
		}
		cur = b
	}
	var started *time.Time
	if !startedAt.IsZero() {
		started = &startedAt
	}
	_, err := db.Exec(ctx, `INSERT INTO playback (id, status, current, started_at, updated_at)
		VALUES (true, $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, current = EXCLUDED.current,
			started_at = EXCLUDED.started_at, updated_at = now()`, string(status), cur, started)
	return err
}

func (p *PostgresStore) Transition(ctx context.Context, ended *HistoryEntry, next *Item, status Status, current *Item, startedAt time.Time) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if ended != nil {
			item, err := json.Marshal(ended.Item)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO history (item, started_at, ended_at, end_reason, ended_by_id, error)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				item, ended.StartedAt, ended.EndedAt, string(ended.EndReason), ended.EndedByID, ended.Error); err != nil {
				return err
			}
		}
		if next != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM queue_items WHERE id = $1`, next.ID); err != nil {
				return err
			}
		}
		return setPlayback(ctx, tx, status, current, startedAt)
	})
}

func (p *PostgresStore) History(ctx context.Context, limit int) ([]*HistoryEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT item, started_at, ended_at, end_reason, ended_by_id, error
		FROM history ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*HistoryEntry
	for rows.Next() {
		h := &HistoryEntry{Item: &Item{}}
		var item []byte
		var reason string
		if err := rows.Scan(&item, &h.StartedAt, &h.EndedAt, &reason, &h.EndedByID, &h.Error); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(item, h.Item); err != nil {
			return nil, fmt.Errorf("decode history item: %w", err)
		}
		h.EndReason = EndReason(reason)
		out = append(out, h)
	}
	return out, rows.Err()
}
