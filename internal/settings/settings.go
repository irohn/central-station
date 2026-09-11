// Package settings is a small key/value store for runtime configuration that
// admins change from the UI (as opposed to environment variables).
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("setting not found")

// Store persists JSON documents by key.
type Store interface {
	Get(ctx context.Context, key string) (json.RawMessage, error)
	Set(ctx context.Context, key string, value json.RawMessage) error
}

// Memory is an in-memory Store (tests, or no DATABASE_URL).
type Memory struct {
	mu   sync.Mutex
	data map[string]json.RawMessage
}

// NewMemory creates an empty store.
func NewMemory() *Memory { return &Memory{data: map[string]json.RawMessage{}} }

func (m *Memory) Get(_ context.Context, key string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append(json.RawMessage(nil), v...), nil
}

func (m *Memory) Set(_ context.Context, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append(json.RawMessage(nil), value...)
	return nil
}

// Postgres stores settings in the settings table.
type Postgres struct{ pool *pgxpool.Pool }

// NewPostgres wraps a connection pool.
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (p *Postgres) Get(ctx context.Context, key string) (json.RawMessage, error) {
	var v []byte
	err := p.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(v), nil
}

func (p *Postgres) Set(ctx context.Context, key string, value json.RawMessage) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, []byte(value))
	return err
}
