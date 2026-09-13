// Package alerts schedules announcements: an Alert says when (a cron
// expression in a timezone) and what (a clip, repeat count and chime).
// Only time-based alerts exist today; Type is there so event-driven ones
// can be added without changing the shape.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// TypeCron is the only alert type for now.
const TypeCron = "cron"

// MaxLoops mirrors the broadcast cap.
const MaxLoops = 10

var (
	ErrNotFound = errors.New("alert not found")
)

// ValidationError reports bad input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Alert is a scheduled announcement.
type Alert struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Type     string `json:"type"`     // "cron"
	Schedule string `json:"schedule"` // 5-field cron: minute hour day-of-month month day-of-week
	Timezone string `json:"timezone"` // IANA name the schedule is evaluated in
	ClipID   string `json:"clipId"`
	Loops    int    `json:"loops"`
	ChimeID  string `json:"chimeId"` // "" = default chime, "none" = no chime, or a chime clip id
	ZoneID   string `json:"zoneId"`  // "" = every zone, otherwise one zone

	CreatedByID   string     `json:"createdById,omitempty"`
	CreatedByName string     `json:"createdByName,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastFiredAt   *time.Time `json:"lastFiredAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	// NextFireAt is computed, not stored.
	NextFireAt *time.Time `json:"nextFireAt,omitempty"`
}

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ParseSchedule validates a 5-field cron expression.
func ParseSchedule(spec string) (cron.Schedule, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, &ValidationError{Msg: "schedule is required"}
	}
	s, err := parser.Parse(spec)
	if err != nil {
		return nil, &ValidationError{Msg: fmt.Sprintf("invalid schedule %q: %v (expected: minute hour day-of-month month day-of-week)", spec, err)}
	}
	return s, nil
}

// Validate checks the fields that do not need other stores.
func (a *Alert) Validate() error {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return &ValidationError{Msg: "name is required"}
	}
	if a.Type == "" {
		a.Type = TypeCron
	}
	if a.Type != TypeCron {
		return &ValidationError{Msg: fmt.Sprintf("unsupported alert type %q (only %q for now)", a.Type, TypeCron)}
	}
	if _, err := ParseSchedule(a.Schedule); err != nil {
		return err
	}
	a.Schedule = strings.TrimSpace(a.Schedule)
	if a.Timezone == "" {
		return &ValidationError{Msg: "timezone is required"}
	}
	if _, err := time.LoadLocation(a.Timezone); err != nil {
		return &ValidationError{Msg: fmt.Sprintf("unknown timezone %q", a.Timezone)}
	}
	if strings.TrimSpace(a.ClipID) == "" {
		return &ValidationError{Msg: "clipId is required"}
	}
	if a.Loops == 0 {
		a.Loops = 1
	}
	if a.Loops < 1 || a.Loops > MaxLoops {
		return &ValidationError{Msg: fmt.Sprintf("loops must be between 1 and %d", MaxLoops)}
	}
	return nil
}

// Next returns the first fire time strictly after t.
func (a *Alert) Next(t time.Time) (time.Time, error) {
	s, err := ParseSchedule(a.Schedule)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(a.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	return s.Next(t.In(loc)), nil
}

// Store persists alerts.
type Store interface {
	List(ctx context.Context) ([]*Alert, error)
	Get(ctx context.Context, id string) (*Alert, error)
	Insert(ctx context.Context, a *Alert) error
	Update(ctx context.Context, a *Alert) error
	Delete(ctx context.Context, id string) error
	// MarkFired records a firing (or its failure).
	MarkFired(ctx context.Context, id string, at time.Time, errMsg string) error
}

// Memory is an in-memory Store.
type Memory struct {
	mu     sync.RWMutex
	alerts map[string]*Alert
}

// NewMemory creates an empty store.
func NewMemory() *Memory { return &Memory{alerts: map[string]*Alert{}} }

func (m *Memory) List(context.Context) ([]*Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Alert, 0, len(m.alerts))
	for _, a := range m.alerts {
		cp := *a
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) Get(_ context.Context, id string) (*Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.alerts[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (m *Memory) Insert(_ context.Context, a *Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *a
	m.alerts[a.ID] = &cp
	return nil
}

func (m *Memory) Update(_ context.Context, a *Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.alerts[a.ID]; !ok {
		return ErrNotFound
	}
	cp := *a
	m.alerts[a.ID] = &cp
	return nil
}

func (m *Memory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.alerts[id]; !ok {
		return ErrNotFound
	}
	delete(m.alerts, id)
	return nil
}

func (m *Memory) MarkFired(_ context.Context, id string, at time.Time, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.alerts[id]
	if !ok {
		return ErrNotFound
	}
	t := at
	a.LastFiredAt = &t
	a.LastError = errMsg
	return nil
}
