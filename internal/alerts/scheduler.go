package alerts

import (
	"context"
	"log/slog"
	"time"
)

// Fire delivers one alert (builds and sends its broadcast).
type Fire func(ctx context.Context, a *Alert) error

// Scheduler fires enabled alerts when their schedule is due. A single
// instance runs inside the API; state is the alerts' LastFiredAt.
type Scheduler struct {
	Store  Store
	Fire   Fire
	Logger *slog.Logger
	// Interval between checks (default 30s). Schedules are minute-granular.
	Interval time.Duration
	// Grace bounds how late a firing may be. Alerts that fell due longer ago
	// (e.g. while the API was down) are skipped, not replayed. Default 5m.
	Grace time.Duration
	Now   func() time.Time
}

// Run blocks until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	s.defaults()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}

func (s *Scheduler) defaults() {
	if s.Interval == 0 {
		s.Interval = 30 * time.Second
	}
	if s.Grace == 0 {
		s.Grace = 5 * time.Minute
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
}

// Tick fires every alert that is due right now. Exported for tests.
func (s *Scheduler) Tick(ctx context.Context) {
	s.defaults()
	now := s.Now()
	list, err := s.Store.List(ctx)
	if err != nil {
		s.Logger.Error("alerts: list", "err", err)
		return
	}
	for _, a := range list {
		if !a.Enabled {
			continue
		}
		due, ok := s.dueAt(a, now)
		if !ok {
			continue
		}
		err := s.Fire(ctx, a)
		msg := ""
		if err != nil {
			msg = err.Error()
			s.Logger.Warn("alert failed", "alert", a.Name, "id", a.ID, "err", err)
		} else {
			s.Logger.Info("alert fired", "alert", a.Name, "id", a.ID, "scheduled", due.Format(time.RFC3339))
		}
		// Record the *scheduled* time so the next occurrence is computed from it.
		if err := s.Store.MarkFired(ctx, a.ID, due, msg); err != nil {
			s.Logger.Error("alerts: mark fired", "id", a.ID, "err", err)
		}
	}
}

// dueAt returns the occurrence the alert should fire for now, if any: the
// first schedule time after the last firing (or the alert's creation) that
// is <= now, provided it is not older than Grace. Older missed occurrences
// are skipped by fast-forwarding to the most recent one.
func (s *Scheduler) dueAt(a *Alert, now time.Time) (time.Time, bool) {
	from := a.CreatedAt
	if a.LastFiredAt != nil && a.LastFiredAt.After(from) {
		from = *a.LastFiredAt
	}
	if a.UpdatedAt.After(from) {
		from = a.UpdatedAt // a rescheduled alert starts counting from the change
	}
	next, err := a.Next(from)
	if err != nil {
		return time.Time{}, false
	}
	if next.After(now) {
		return time.Time{}, false
	}
	// Skip forward past stale occurrences.
	latest := next
	for {
		n, err := a.Next(latest)
		if err != nil || n.After(now) {
			break
		}
		latest = n
	}
	if now.Sub(latest) > s.Grace {
		return time.Time{}, false
	}
	return latest, true
}

// NextFire computes the next occurrence after now for display.
func NextFire(a *Alert, now time.Time) *time.Time {
	if !a.Enabled {
		return nil
	}
	n, err := a.Next(now)
	if err != nil {
		return nil
	}
	return &n
}
