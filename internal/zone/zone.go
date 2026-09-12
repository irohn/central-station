// Package zone defines zones: one player node with its own queue, playback
// state, history, default playlist and volume. The web app switches between
// them; each player container authenticates with its zone's token.
package zone

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// DefaultID is the zone that pre-zone data was migrated into and that the
// legacy PLAYER_TOKEN environment variable authenticates.
const DefaultID = "00000000-0000-4000-8000-000000000001"

// DefaultName is used when the default zone has to be created.
const DefaultName = "Office"

// MaxNameLen bounds zone names.
const MaxNameLen = 60

var (
	ErrNotFound = errors.New("zone not found")
	ErrLastZone = errors.New("the last zone cannot be deleted")
)

// ValidationError reports bad input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Zone is a player node.
type Zone struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	HasToken  bool      `json:"hasToken"` // a player token has been issued (legacy env token does not count)
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	tokenHash string
}

// TokenHash returns the stored hash (empty when no token was issued).
func (z *Zone) TokenHash() string { return z.tokenHash }

// Store persists zones.
type Store interface {
	List(ctx context.Context) ([]*Zone, error)
	Get(ctx context.Context, id string) (*Zone, error)
	// Create inserts z (ID, Name, Position set by the caller). tokenHash may be empty.
	Create(ctx context.Context, z *Zone, tokenHash string) error
	Rename(ctx context.Context, id, name string) (*Zone, error)
	SetTokenHash(ctx context.Context, id, hash string) error
	FindByTokenHash(ctx context.Context, hash string) (*Zone, error)
	// Delete removes the zone and everything scoped to it.
	Delete(ctx context.Context, id string) error
}

// CleanName validates and normalises a zone name.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &ValidationError{Msg: "name is required"}
	}
	if len(name) > MaxNameLen {
		return "", &ValidationError{Msg: "name is too long"}
	}
	return name, nil
}

// NewToken generates a player token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("zone: crypto/rand failed: " + err.Error())
	}
	return "csp_" + base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex SHA-256 of a token; only hashes are stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// SettingsPrefix namespaces a zone's keys in the settings store.
func SettingsPrefix(id string) string { return "zone:" + id + ":" }
