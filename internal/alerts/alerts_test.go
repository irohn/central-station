package alerts

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mk(name, spec string, created time.Time) *Alert {
	return &Alert{ID: name, Name: name, Enabled: true, Type: TypeCron, Schedule: spec, Timezone: "Asia/Jerusalem", ClipID: "c", Loops: 1, CreatedAt: created, UpdatedAt: created}
}

func TestValidate(t *testing.T) {
	good := mk("lunch", "0 12 * * 1-5", time.Now())
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*Alert{
		{Name: "", Schedule: "0 12 * * *", Timezone: "UTC", ClipID: "c"},
		{Name: "x", Schedule: "0 12 * *", Timezone: "UTC", ClipID: "c"},
		{Name: "x", Schedule: "0 12 * * *", Timezone: "Mars/Olympus", ClipID: "c"},
		{Name: "x", Schedule: "0 12 * * *", Timezone: "UTC", ClipID: ""},
		{Name: "x", Schedule: "0 12 * * *", Timezone: "UTC", ClipID: "c", Loops: 11},
		{Name: "x", Type: "webhook", Schedule: "0 12 * * *", Timezone: "UTC", ClipID: "c"},
	} {
		var ve *ValidationError
		if err := bad.Validate(); !errors.As(err, &ve) {
			t.Fatalf("%+v: expected validation error, got %v", bad, err)
		}
	}
}

func TestNextInTimezone(t *testing.T) {
	a := mk("noon", "0 12 * * *", time.Time{})
	// 09:00 UTC in September is 12:00 in Jerusalem (UTC+3): the next noon is today.
	from := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	n, err := a.Next(from)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Equal(time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %s", n)
	}
}

func TestSchedulerFiresOncePerOccurrence(t *testing.T) {
	ctx := context.Background()
	st := NewMemory()
	created := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	_ = st.Insert(ctx, mk("noon", "0 12 * * *", created))      // 09:00 UTC
	_ = st.Insert(ctx, mk("quarter", "*/15 * * * *", created)) // every 15 min
	off := mk("off", "* * * * *", created)
	off.Enabled = false
	_ = st.Insert(ctx, off)

	var fired []string
	now := created
	s := &Scheduler{Store: st, Now: func() time.Time { return now }, Fire: func(_ context.Context, a *Alert) error { fired = append(fired, a.Name); return nil }}

	now = created.Add(10 * time.Second) // 08:00:10: nothing due yet (created at 08:00 exactly is not "after")
	s.Tick(ctx)
	now = created.Add(15*time.Minute + 20*time.Second) // 08:15:20
	s.Tick(ctx)
	s.Tick(ctx)                                       // same minute again: must not double fire
	now = created.Add(60*time.Minute + 5*time.Second) // 09:00:05: noon (Jerusalem) and quarter (09:00)
	s.Tick(ctx)
	if got := join(fired); got != "quarter quarter noon" && got != "quarter noon quarter" {
		t.Fatalf("fired = %v", fired)
	}
	a, _ := st.Get(ctx, "quarter")
	if a.LastFiredAt == nil || !a.LastFiredAt.Equal(created.Add(60*time.Minute)) {
		t.Fatalf("last fired = %v", a.LastFiredAt)
	}
}

func TestSchedulerSkipsStaleOccurrences(t *testing.T) {
	ctx := context.Background()
	st := NewMemory()
	created := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	_ = st.Insert(ctx, mk("hourly", "0 * * * *", created))
	var fired int
	now := created.Add(3*time.Hour + 30*time.Second) // API was "down" for 3 hours; only the latest (11:00) is within grace
	s := &Scheduler{Store: st, Now: func() time.Time { return now }, Fire: func(context.Context, *Alert) error { fired++; return nil }}
	s.Tick(ctx)
	if fired != 1 {
		t.Fatalf("fired %d times, want 1", fired)
	}
	a, _ := st.Get(ctx, "hourly")
	if !a.LastFiredAt.Equal(created.Add(3 * time.Hour)) {
		t.Fatalf("last fired = %v", a.LastFiredAt)
	}
	// Ten minutes past an occurrence: beyond grace, skipped.
	_ = st.Insert(ctx, mk("late", "0 * * * *", created))
	now = created.Add(4*time.Hour + 10*time.Minute)
	fired = 0
	s.Tick(ctx)
	if fired != 0 {
		t.Fatalf("stale occurrence fired")
	}
}

func TestSchedulerRecordsErrors(t *testing.T) {
	ctx := context.Background()
	st := NewMemory()
	created := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	_ = st.Insert(ctx, mk("m", "* * * * *", created))
	now := created.Add(61 * time.Second)
	s := &Scheduler{Store: st, Now: func() time.Time { return now }, Fire: func(context.Context, *Alert) error { return errors.New("player offline") }}
	s.Tick(ctx)
	a, _ := st.Get(ctx, "m")
	if a.LastError != "player offline" || a.LastFiredAt == nil {
		t.Fatalf("alert = %+v", a)
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}
