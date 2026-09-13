package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"central-station/internal/queue"
	"central-station/internal/settings"
)

// DefaultVolume is used until a manager sets one.
const DefaultVolume = 80

const (
	volumeKey = "volume"
	outputKey = "audio-device"
)

// ErrBadVolume is returned for levels outside 0-100.
var ErrBadVolume = errors.New("volume must be between 0 and 100")

// ErrUnknownDevice is returned when the requested output is not one the
// connected player reported.
var ErrUnknownDevice = errors.New("unknown audio device")

// ErrPlayerOffline is returned when an announcement cannot be delivered.
var ErrPlayerOffline = errors.New("no player is connected")

// DuckPercent is the music level (relative) while an announcement plays.
const DuckPercent = 15

// MaxBroadcastLoops caps how many times one clip repeats.
const MaxBroadcastLoops = 10

// Broadcast is a queued or playing announcement.
type Broadcast struct {
	ID       string
	ClipID   string
	ClipName string
	ClipURL  string
	ChimeURL string
	Loops    int
	ByID     string
	ByName   string
}

const (
	writeTimeout = 5 * time.Second
	// playerReadTimeout: the player sends progress every 2s while playing and
	// a heartbeat when idle, so silence this long means it is gone.
	playerReadTimeout = 30 * time.Second
)

// Hub owns the single player connection and the set of browser subscribers.
type Hub struct {
	queue  *queue.Service
	logger *slog.Logger
	// OriginPatterns are the browser origins allowed to open spectator sockets.
	OriginPatterns []string
	// VolumeStore persists the volume across restarts. Optional.
	VolumeStore settings.Store
	// OnStatusChange, when set, is called (without the lock held) whenever the
	// player status changes in a way other zones' viewers care about: connect,
	// disconnect, hello, volume, announcements. Progress ticks do not fire it.
	OnStatusChange func()

	mu          sync.Mutex
	player      *websocket.Conn
	status      Status
	volume      int
	audioDevice string // requested output; "auto" = OS default
	spectators  map[*websocket.Conn]struct{}
	active      *Broadcast
	activeAt    time.Time
	pending     []*Broadcast
}

// SendBroadcast queues an announcement; it starts at once when none is
// playing. Returns its position (0 = playing now).
func (h *Hub) SendBroadcast(b *Broadcast) (int, error) {
	if b.Loops < 1 {
		b.Loops = 1
	}
	if b.Loops > MaxBroadcastLoops {
		b.Loops = MaxBroadcastLoops
	}
	h.mu.Lock()
	if h.player == nil {
		h.mu.Unlock()
		return 0, ErrPlayerOffline
	}
	h.pending = append(h.pending, b)
	pos := len(h.pending) // place in the waiting line...
	if h.active == nil {
		pos = 0 // ...or starting right now
	}
	h.mu.Unlock()
	h.startNextBroadcast()
	return pos, nil
}

// startNextBroadcast sends the head of the queue to the player if idle.
func (h *Hub) startNextBroadcast() {
	h.mu.Lock()
	if h.active != nil || len(h.pending) == 0 || h.player == nil {
		h.mu.Unlock()
		return
	}
	b := h.pending[0]
	h.pending = h.pending[1:]
	h.active = b
	h.activeAt = time.Now()
	h.status.Broadcast = &BroadcastStatus{ID: b.ID, ClipID: b.ClipID, ClipName: b.ClipName, Loops: b.Loops, ByID: b.ByID, ByName: b.ByName, StartedAt: h.activeAt}
	h.status.BroadcastsQueued = len(h.pending)
	p := h.player
	h.mu.Unlock()
	h.send(p, ServerMessage{Type: "broadcast", Broadcast: &BroadcastCommand{ID: b.ID, ClipURL: b.ClipURL, ChimeURL: b.ChimeURL, Loops: b.Loops, DuckPercent: DuckPercent}})
	h.broadcastPlayer()
}

// Broadcasts returns the playing announcement (nil if none) and the queue.
func (h *Hub) Broadcasts() (*BroadcastStatus, []BroadcastStatus) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var cur *BroadcastStatus
	if h.status.Broadcast != nil {
		c := *h.status.Broadcast
		cur = &c
	}
	q := make([]BroadcastStatus, 0, len(h.pending))
	for _, b := range h.pending {
		q = append(q, BroadcastStatus{ID: b.ID, ClipID: b.ClipID, ClipName: b.ClipName, Loops: b.Loops, ByID: b.ByID, ByName: b.ByName})
	}
	return cur, q
}

// NewHub creates a hub bound to the queue. It installs itself as the queue's
// OnChange hook.
func NewHub(q *queue.Service, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Hub{queue: q, logger: logger, volume: DefaultVolume, audioDevice: AutoDevice, spectators: map[*websocket.Conn]struct{}{}}
	h.status.Volume = DefaultVolume
	h.status.AudioDevice = AutoDevice
	h.status.AudioDevices = []AudioDevice{}
	q.OnChange = h.QueueChanged
	return h
}

// QueueChanged pushes the desired state to the player and the snapshot to
// browsers. It is the queue's OnChange hook; wrap it to add more listeners.
func (h *Hub) QueueChanged() { h.onQueueChange() }

// BroadcastEvent sends an event to every browser watching this hub.
func (h *Hub) BroadcastEvent(ev Event) { h.broadcast(ev) }

// DisconnectPlayer drops the current player connection (it will reconnect
// and re-authenticate). Browsers stay connected.
func (h *Hub) DisconnectPlayer(reason string) {
	h.mu.Lock()
	p := h.player
	h.mu.Unlock()
	if p != nil {
		p.Close(websocket.StatusPolicyViolation, reason)
	}
}

// Close disconnects the player and every browser (zone deleted).
func (h *Hub) Close() {
	h.mu.Lock()
	p := h.player
	h.player = nil
	conns := make([]*websocket.Conn, 0, len(h.spectators))
	for c := range h.spectators {
		conns = append(conns, c)
	}
	h.spectators = map[*websocket.Conn]struct{}{}
	h.mu.Unlock()
	if p != nil {
		p.Close(websocket.StatusGoingAway, "zone removed")
	}
	for _, c := range conns {
		c.Close(websocket.StatusGoingAway, "zone removed")
	}
}

// LoadVolume reads the persisted volume and output device from VolumeStore, if any.
func (h *Hub) LoadVolume(ctx context.Context) error {
	if h.VolumeStore == nil {
		return nil
	}
	raw, err := h.VolumeStore.Get(ctx, volumeKey)
	switch {
	case errors.Is(err, settings.ErrNotFound):
	case err != nil:
		return err
	default:
		var v int
		if err := json.Unmarshal(raw, &v); err != nil || v < 0 || v > 100 {
			return fmt.Errorf("stored volume %q is invalid", string(raw))
		}
		h.mu.Lock()
		h.volume = v
		h.status.Volume = v
		h.mu.Unlock()
	}
	raw, err = h.VolumeStore.Get(ctx, outputKey)
	switch {
	case errors.Is(err, settings.ErrNotFound):
	case err != nil:
		return err
	default:
		var d string
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("stored audio device %q is invalid", string(raw))
		}
		if d == "" {
			d = AutoDevice
		}
		h.mu.Lock()
		h.audioDevice = d
		h.status.AudioDevice = d
		h.mu.Unlock()
	}
	return nil
}

// AudioDevice returns the requested output id.
func (h *Hub) AudioDevice() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.audioDevice
}

// SetAudioDevice selects the output. id must be AutoDevice or one of the
// devices the connected player reported; with no player connected any id is
// accepted and validated when one connects. The choice is persisted.
func (h *Hub) SetAudioDevice(ctx context.Context, id string) error {
	if id == "" {
		id = AutoDevice
	}
	h.mu.Lock()
	known := id == AutoDevice || len(h.status.AudioDevices) == 0
	for _, d := range h.status.AudioDevices {
		if d.ID == id {
			known = true
			break
		}
	}
	h.mu.Unlock()
	if !known {
		return fmt.Errorf("%w: %q", ErrUnknownDevice, id)
	}
	if h.VolumeStore != nil {
		raw, _ := json.Marshal(id)
		if err := h.VolumeStore.Set(ctx, outputKey, raw); err != nil {
			return err
		}
	}
	h.mu.Lock()
	h.audioDevice = id
	h.status.AudioDevice = id
	h.status.AudioDeviceMissing = false // the player re-reports after applying
	p := h.player
	h.mu.Unlock()
	if p != nil {
		h.send(p, ServerMessage{Type: "state", State: h.desired()})
	}
	h.broadcastPlayer()
	return nil
}

// Volume returns the current level (0-100).
func (h *Hub) Volume() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.volume
}

// SetVolume persists the level, pushes it to the player and tells browsers.
func (h *Hub) SetVolume(ctx context.Context, v int) error {
	if v < 0 || v > 100 {
		return ErrBadVolume
	}
	if h.VolumeStore != nil {
		if err := h.VolumeStore.Set(ctx, volumeKey, json.RawMessage(fmt.Sprint(v))); err != nil {
			return err
		}
	}
	h.mu.Lock()
	h.volume = v
	h.status.Volume = v
	p := h.player
	h.mu.Unlock()
	if p != nil {
		h.send(p, ServerMessage{Type: "state", State: h.desired()})
	}
	h.broadcastPlayer()
	return nil
}

// Status returns a copy of the player status.
func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *Hub) desired() *DesiredState {
	s := h.queue.Snapshot()
	return &DesiredState{Status: s.Status, Current: s.Current, Volume: h.Volume(), AudioDevice: h.AudioDevice()}
}

// onQueueChange pushes the desired state to the player and the snapshot to browsers.
func (h *Hub) onQueueChange() {
	h.mu.Lock()
	p := h.player
	st := h.status
	h.mu.Unlock()
	if p != nil {
		h.send(p, ServerMessage{Type: "state", State: h.desired()})
	}
	snap := h.queue.Snapshot()
	h.broadcast(Event{Type: "queue", Queue: &snap, Player: &st})
}

func (h *Hub) send(c *websocket.Conn, v any) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := wsjson.Write(ctx, c, v); err != nil {
		h.logger.Debug("ws write failed", "err", err)
	}
}

func (h *Hub) broadcast(ev Event) {
	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.spectators))
	for c := range h.spectators {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		h.send(c, ev)
	}
}

// ServePlayer upgrades the request to the player socket. Authentication must
// already have happened. A new player replaces a previous one.
func (h *Hub) ServePlayer(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.OriginPatterns})
	if err != nil {
		h.logger.Warn("player ws accept", "err", err)
		return
	}
	c.SetReadLimit(64 << 10)

	h.mu.Lock()
	old := h.player
	h.player = c
	h.status = Status{Online: true, ConnectedAt: time.Now(), LastSeenAt: time.Now(), Volume: h.volume, AudioDevice: h.audioDevice, AudioDevices: []AudioDevice{}}
	h.mu.Unlock()
	if old != nil {
		h.logger.Info("player replaced by a new connection")
		old.Close(websocket.StatusPolicyViolation, "replaced by another player")
	}
	h.logger.Info("player connected", "remote", r.RemoteAddr)
	h.send(c, ServerMessage{Type: "state", State: h.desired()})
	h.broadcastPlayer()
	h.startNextBroadcast()

	defer func() {
		h.mu.Lock()
		if h.player == c {
			h.player = nil
			h.active = nil // whatever was playing died with the connection; queued ones wait
			h.status = Status{Online: false, LastSeenAt: time.Now(), LastError: h.status.LastError, Volume: h.volume, BroadcastsQueued: len(h.pending), AudioDevice: h.audioDevice, AudioDevices: []AudioDevice{}}
		}
		h.mu.Unlock()
		c.Close(websocket.StatusNormalClosure, "")
		h.logger.Info("player disconnected")
		h.broadcastPlayer()
	}()

	for {
		ctx, cancel := context.WithTimeout(r.Context(), playerReadTimeout)
		var msg ClientMessage
		err := wsjson.Read(ctx, c, &msg)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
				h.logger.Debug("player read", "err", err)
			}
			return
		}
		h.handlePlayerMessage(c, msg)
	}
}

func (h *Hub) handlePlayerMessage(c *websocket.Conn, msg ClientMessage) {
	h.mu.Lock()
	if h.player != c {
		h.mu.Unlock()
		return
	}
	h.status.LastSeenAt = time.Now()
	switch msg.Type {
	case "hello":
		h.status.Name = msg.Name
		h.status.Version = msg.Version
		h.mu.Unlock()
		h.broadcastPlayer()
		return
	case "devices":
		h.status.AudioDevices = msg.Devices
		if h.status.AudioDevices == nil {
			h.status.AudioDevices = []AudioDevice{}
		}
		h.status.ActiveAudioDevice = msg.ActiveDevice
		h.status.AudioDeviceMissing = msg.DeviceMissing
		h.mu.Unlock()
		h.broadcastPlayer()
		return
	case "progress":
		h.status.ItemID = msg.ItemID
		h.status.Position = msg.Position
		h.status.Duration = msg.Duration
		st := h.status
		h.mu.Unlock()
		h.broadcast(Event{Type: "progress", Player: &st})
		return
	case "broadcast_ended":
		if h.active == nil || h.active.ID != msg.BroadcastID {
			h.mu.Unlock()
			return
		}
		if msg.Reason == "error" {
			h.logger.Warn("broadcast failed", "id", msg.BroadcastID, "err", msg.Error)
			h.status.LastError = "announcement failed: " + msg.Error
		}
		h.active = nil
		h.status.Broadcast = nil
		h.status.BroadcastsQueued = len(h.pending)
		h.mu.Unlock()
		h.broadcastPlayer()
		h.startNextBroadcast()
		return
	case "ended":
		if msg.Reason == "error" {
			h.status.LastError = msg.Error
		} else {
			h.status.LastError = ""
		}
		h.status.Position = 0
		h.mu.Unlock()
		cur := h.queue.Current()
		if cur == nil || cur.ID != msg.ItemID {
			// Stale report (we already moved on); resend desired state.
			h.send(c, ServerMessage{Type: "state", State: h.desired()})
			return
		}
		if msg.Reason == "error" {
			h.logger.Warn("player failed to play item", "itemId", msg.ItemID, "err", msg.Error)
			if _, err := h.queue.Fail(context.Background(), "player", msg.Error); err != nil {
				h.logger.Error("record playback error", "err", err)
			}
		} else if _, err := h.queue.Complete(context.Background(), "player"); err != nil {
			h.logger.Error("advance queue", "err", err)
		}
		return
	default:
		h.mu.Unlock()
	}
}

func (h *Hub) broadcastPlayer() {
	st := h.Status()
	h.broadcast(Event{Type: "player", Player: &st})
	if h.OnStatusChange != nil {
		h.OnStatusChange()
	}
}

// ServeSpectator upgrades a browser request to a read-only event stream.
func (h *Hub) ServeSpectator(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.OriginPatterns})
	if err != nil {
		h.logger.Warn("spectator ws accept", "err", err)
		return
	}
	c.SetReadLimit(4 << 10)
	h.mu.Lock()
	h.spectators[c] = struct{}{}
	st := h.status
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.spectators, c)
		h.mu.Unlock()
		c.Close(websocket.StatusNormalClosure, "")
	}()

	snap := h.queue.Snapshot()
	h.send(c, Event{Type: "queue", Queue: &snap, Player: &st})

	// Browsers send nothing; reading keeps ping/pong and close frames flowing.
	for {
		if _, _, err := c.Read(r.Context()); err != nil {
			return
		}
	}
}
