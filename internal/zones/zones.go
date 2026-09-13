// Package zones runs one queue, player hub and default playlist per zone and
// keeps them in step with the zone store: creating a zone spins up its
// runtime, deleting one tears it down. Handlers look up a Runtime by zone id.
package zones

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/google/uuid"

	"central-station/internal/fallback"
	"central-station/internal/player"
	"central-station/internal/queue"
	"central-station/internal/settings"
	"central-station/internal/zone"
)

// ErrBadToken is returned when no zone matches a player token.
var ErrBadToken = errors.New("invalid player token")

// Runtime is everything that runs for one zone.
type Runtime struct {
	Zone     *zone.Zone
	Queue    *queue.Service
	Hub      *player.Hub
	Fallback *fallback.Manager
}

// Deps configure the manager.
type Deps struct {
	Store    zone.Store
	Settings settings.Store
	Resolver fallback.Resolver
	Logger   *slog.Logger
	// NewQueueStore returns the queue store for a zone (Postgres scoped to
	// the zone, or a fresh memory store).
	NewQueueStore func(zoneID string) queue.Store
	// OriginPatterns are passed to every hub for WebSocket origin checks.
	OriginPatterns []string
	// LegacyToken (PLAYER_TOKEN) authenticates a player for the default zone.
	LegacyToken string
}

// Manager owns the runtimes.
type Manager struct {
	deps Deps

	mu       sync.RWMutex
	runtimes map[string]*Runtime
}

// Summary is the per-zone view for the zone switcher, embedded in "zones" events.
type Summary struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Position  int           `json:"position"`
	HasToken  bool          `json:"hasToken"`
	Status    queue.Status  `json:"status"`
	Current   *queue.Item   `json:"current"`
	Queued    int           `json:"queued"`
	Player    player.Status `json:"player"`
	IsDefault bool          `json:"isDefault"`
}

// New loads every zone and starts its runtime. If no zone exists the default
// one is created so pre-zone deployments keep working unchanged.
func New(ctx context.Context, deps Deps) (*Manager, error) {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Store == nil || deps.Settings == nil || deps.NewQueueStore == nil {
		return nil, errors.New("zones: Store, Settings and NewQueueStore are required")
	}
	m := &Manager{deps: deps, runtimes: map[string]*Runtime{}}
	list, err := deps.Store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	if len(list) == 0 {
		z := &zone.Zone{ID: zone.DefaultID, Name: zone.DefaultName, Position: 1}
		if err := deps.Store.Create(ctx, z, ""); err != nil {
			return nil, fmt.Errorf("create default zone: %w", err)
		}
		list = []*zone.Zone{z}
	}
	for _, z := range list {
		rt, err := m.start(ctx, z)
		if err != nil {
			return nil, fmt.Errorf("zone %q: %w", z.Name, err)
		}
		m.runtimes[z.ID] = rt
	}
	return m, nil
}

func (m *Manager) start(ctx context.Context, z *zone.Zone) (*Runtime, error) {
	logger := m.deps.Logger.With("zone", z.Name)
	zs := settings.WithPrefix(m.deps.Settings, zone.SettingsPrefix(z.ID))

	q, err := queue.New(ctx, m.deps.NewQueueStore(z.ID))
	if err != nil {
		return nil, err
	}
	hub := player.NewHub(q, logger)
	hub.OriginPatterns = m.deps.OriginPatterns
	hub.VolumeStore = zs
	if err := hub.LoadVolume(ctx); err != nil {
		logger.Warn("load volume; using default", "err", err)
	}
	hub.OnStatusChange = m.zonesChanged
	q.OnChange = func() {
		hub.QueueChanged()
		m.zonesChanged()
	}

	fb, err := fallback.New(ctx, zs, m.deps.Resolver, logger)
	if err != nil {
		return nil, err
	}
	fb.OnChange = q.Notify
	q.SetFallback(fb)
	return &Runtime{Zone: z, Queue: q, Hub: hub, Fallback: fb}, nil
}

// List returns runtimes in display order.
func (m *Manager) List() []*Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Runtime, 0, len(m.runtimes))
	for _, rt := range m.runtimes {
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Zone, out[j].Zone
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return out
}

// Get returns a runtime by zone id.
func (m *Manager) Get(id string) (*Runtime, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt, ok := m.runtimes[id]
	return rt, ok
}

// Default returns the default zone's runtime, or the first zone when the
// default was deleted.
func (m *Manager) Default() *Runtime {
	if rt, ok := m.Get(zone.DefaultID); ok {
		return rt
	}
	list := m.List()
	if len(list) == 0 {
		return nil
	}
	return list[0]
}

// Create adds a zone and starts its runtime. The returned token is shown once.
func (m *Manager) Create(ctx context.Context, name string) (*Runtime, string, error) {
	name, err := zone.CleanName(name)
	if err != nil {
		return nil, "", err
	}
	token := zone.NewToken()
	pos := 0
	for _, rt := range m.List() {
		if rt.Zone.Position > pos {
			pos = rt.Zone.Position
		}
	}
	z := &zone.Zone{ID: uuid.NewString(), Name: name, Position: pos + 1}
	if err := m.deps.Store.Create(ctx, z, zone.HashToken(token)); err != nil {
		return nil, "", err
	}
	rt, err := m.start(ctx, z)
	if err != nil {
		_ = m.deps.Store.Delete(ctx, z.ID)
		return nil, "", err
	}
	m.mu.Lock()
	m.runtimes[z.ID] = rt
	m.mu.Unlock()
	m.deps.Logger.Info("zone created", "zone", name, "id", z.ID)
	m.zonesChanged()
	return rt, token, nil
}

// Rename changes a zone's display name.
func (m *Manager) Rename(ctx context.Context, id, name string) (*zone.Zone, error) {
	name, err := zone.CleanName(name)
	if err != nil {
		return nil, err
	}
	rt, ok := m.Get(id)
	if !ok {
		return nil, zone.ErrNotFound
	}
	z, err := m.deps.Store.Rename(ctx, id, name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	rt.Zone = z
	m.mu.Unlock()
	m.zonesChanged()
	return z, nil
}

// RotateToken issues a new player token for the zone; the old one stops
// working and a connected player is disconnected so it must re-authenticate.
func (m *Manager) RotateToken(ctx context.Context, id string) (string, error) {
	rt, ok := m.Get(id)
	if !ok {
		return "", zone.ErrNotFound
	}
	token := zone.NewToken()
	if err := m.deps.Store.SetTokenHash(ctx, id, zone.HashToken(token)); err != nil {
		return "", err
	}
	z, err := m.deps.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	rt.Zone = z
	m.mu.Unlock()
	rt.Hub.DisconnectPlayer("token rotated")
	m.zonesChanged()
	return token, nil
}

// Delete removes a zone, its data and its runtime. The last zone stays.
func (m *Manager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	rt, ok := m.runtimes[id]
	if !ok {
		m.mu.Unlock()
		return zone.ErrNotFound
	}
	if len(m.runtimes) == 1 {
		m.mu.Unlock()
		return zone.ErrLastZone
	}
	delete(m.runtimes, id)
	m.mu.Unlock()
	if err := m.deps.Store.Delete(ctx, id); err != nil {
		m.mu.Lock()
		m.runtimes[id] = rt
		m.mu.Unlock()
		return err
	}
	rt.Hub.Close()
	m.deps.Logger.Info("zone deleted", "zone", rt.Zone.Name, "id", id)
	m.zonesChanged()
	return nil
}

// Authenticate maps a player token to its zone. The legacy PLAYER_TOKEN
// authenticates the default zone.
func (m *Manager) Authenticate(ctx context.Context, token string) (*Runtime, error) {
	if token == "" {
		return nil, ErrBadToken
	}
	if m.deps.LegacyToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(m.deps.LegacyToken)) == 1 {
		if rt := m.Default(); rt != nil {
			return rt, nil
		}
	}
	z, err := m.deps.Store.FindByTokenHash(ctx, zone.HashToken(token))
	if errors.Is(err, zone.ErrNotFound) {
		return nil, ErrBadToken
	}
	if err != nil {
		return nil, err
	}
	rt, ok := m.Get(z.ID)
	if !ok {
		return nil, ErrBadToken
	}
	return rt, nil
}

// Summaries describes every zone for the switcher.
func (m *Manager) Summaries() []Summary {
	list := m.List()
	out := make([]Summary, 0, len(list))
	for _, rt := range list {
		s := rt.Queue.Snapshot()
		out = append(out, Summary{
			ID: rt.Zone.ID, Name: rt.Zone.Name, Position: rt.Zone.Position, HasToken: rt.Zone.HasToken,
			Status: s.Status, Current: s.Current, Queued: len(s.Items), Player: rt.Hub.Status(),
			IsDefault: rt.Zone.ID == zone.DefaultID,
		})
	}
	return out
}

// zonesChanged pushes the zone summaries to every browser on every hub.
func (m *Manager) zonesChanged() {
	ev := player.Event{Type: "zones", Zones: m.Summaries()}
	for _, rt := range m.List() {
		rt.Hub.BroadcastEvent(ev)
	}
}

// Nudge starts the default playlist in every idle zone (startup).
func (m *Manager) Nudge(ctx context.Context) {
	for _, rt := range m.List() {
		if err := rt.Queue.Nudge(ctx); err != nil {
			m.deps.Logger.Warn("start default playlist", "zone", rt.Zone.Name, "err", err)
		}
	}
}

// Targets returns the runtimes an announcement addressed to zoneID goes to:
// one zone, or all of them when zoneID is empty.
func (m *Manager) Targets(zoneID string) ([]*Runtime, error) {
	if zoneID == "" {
		return m.List(), nil
	}
	rt, ok := m.Get(zoneID)
	if !ok {
		return nil, zone.ErrNotFound
	}
	return []*Runtime{rt}, nil
}

// SendBroadcast delivers an announcement to the target zones. Each target
// gets its own copy (broadcast ids are per zone). It fails only when no
// target has a player online.
func (m *Manager) SendBroadcast(zoneID string, b *player.Broadcast) (delivered []*Runtime, err error) {
	targets, err := m.Targets(zoneID)
	if err != nil {
		return nil, err
	}
	for _, rt := range targets {
		cp := *b
		if len(targets) > 1 {
			cp.ID = uuid.NewString()
		}
		if _, err := rt.Hub.SendBroadcast(&cp); err == nil {
			delivered = append(delivered, rt)
		}
	}
	if len(delivered) == 0 {
		return nil, player.ErrPlayerOffline
	}
	return delivered, nil
}
