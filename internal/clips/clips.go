// Package clips stores short audio outside the music queue: voice recordings,
// text-to-speech output and chimes. Broadcasts and alerts play them.
package clips

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind classifies a clip.
type Kind string

const (
	KindRecording Kind = "recording"
	KindTTS       Kind = "tts"
	KindChime     Kind = "chime"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindRecording || k == KindTTS || k == KindChime }

// MaxBytes caps uploads (a minute of AAC/Opus is well under this).
const MaxBytes = 5 << 20

// MaxDurationSeconds caps recordings.
const MaxDurationSeconds = 60

// Clip is the metadata; audio is fetched separately.
type Clip struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            Kind      `json:"kind"`
	Mime            string    `json:"mime"`
	Size            int       `json:"size"`
	DurationSeconds float64   `json:"durationSeconds,omitempty"`
	Text            string    `json:"text,omitempty"` // TTS source text
	CreatedByID     string    `json:"createdById,omitempty"`
	CreatedByName   string    `json:"createdByName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	Builtin         bool      `json:"builtin"` // shipped with the app; cannot be deleted
}

var ErrNotFound = errors.New("clip not found")

// Store persists clips.
type Store interface {
	List(ctx context.Context) ([]*Clip, error)
	Get(ctx context.Context, id string) (*Clip, error)
	// Audio returns the clip and its bytes.
	Audio(ctx context.Context, id string) (*Clip, []byte, error)
	Insert(ctx context.Context, c *Clip, audio []byte) error
	Rename(ctx context.Context, id, name string) (*Clip, error)
	Delete(ctx context.Context, id string) error
}

// AllowedMimes are the container types the browser produces and mpv plays.
var AllowedMimes = map[string]bool{
	"audio/mp4": true, "audio/aac": true, "audio/x-m4a": true, "audio/m4a": true,
	"audio/webm": true, "audio/ogg": true, "audio/opus": true,
	"audio/wav": true, "audio/x-wav": true, "audio/wave": true,
	"audio/mpeg": true, "audio/mp3": true,
}

// NormalizeMime strips codec parameters ("audio/webm;codecs=opus" -> "audio/webm").
func NormalizeMime(m string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(m, ";", 2)[0]))
}

// Memory is an in-memory Store.
type Memory struct {
	mu    sync.RWMutex
	clips map[string]*Clip
	audio map[string][]byte
}

// NewMemory creates an empty store.
func NewMemory() *Memory { return &Memory{clips: map[string]*Clip{}, audio: map[string][]byte{}} }

func (m *Memory) List(context.Context) ([]*Clip, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Clip, 0, len(m.clips))
	for _, c := range m.clips {
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *Memory) Get(_ context.Context, id string) (*Clip, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clips[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (m *Memory) Audio(_ context.Context, id string) (*Clip, []byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clips[id]
	if !ok {
		return nil, nil, ErrNotFound
	}
	cp := *c
	return &cp, append([]byte(nil), m.audio[id]...), nil
}

func (m *Memory) Insert(_ context.Context, c *Clip, audio []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *c
	m.clips[c.ID] = &cp
	m.audio[c.ID] = append([]byte(nil), audio...)
	return nil
}

func (m *Memory) Rename(_ context.Context, id, name string) (*Clip, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clips[id]
	if !ok {
		return nil, ErrNotFound
	}
	c.Name = name
	cp := *c
	return &cp, nil
}

func (m *Memory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.clips[id]; !ok {
		return ErrNotFound
	}
	delete(m.clips, id)
	delete(m.audio, id)
	return nil
}

// EnsureBuiltins inserts the bundled chimes that are missing from the store.
func EnsureBuiltins(ctx context.Context, s Store) error {
	for _, b := range Builtins() {
		if _, err := s.Get(ctx, b.Clip.ID); err == nil {
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := s.Insert(ctx, &b.Clip, b.Audio); err != nil {
			return err
		}
	}
	return nil
}
