package player

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"central-station/internal/queue"
)

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

	mu         sync.Mutex
	player     *websocket.Conn
	status     Status
	spectators map[*websocket.Conn]struct{}
}

// NewHub creates a hub bound to the queue. It installs itself as the queue's
// OnChange hook.
func NewHub(q *queue.Service, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Hub{queue: q, logger: logger, spectators: map[*websocket.Conn]struct{}{}}
	q.OnChange = h.onQueueChange
	return h
}

// Status returns a copy of the player status.
func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *Hub) desired() *DesiredState {
	s := h.queue.Snapshot()
	return &DesiredState{Status: s.Status, Current: s.Current}
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
	h.status = Status{Online: true, ConnectedAt: time.Now(), LastSeenAt: time.Now()}
	h.mu.Unlock()
	if old != nil {
		h.logger.Info("player replaced by a new connection")
		old.Close(websocket.StatusPolicyViolation, "replaced by another player")
	}
	h.logger.Info("player connected", "remote", r.RemoteAddr)
	h.send(c, ServerMessage{Type: "state", State: h.desired()})
	h.broadcastPlayer()

	defer func() {
		h.mu.Lock()
		if h.player == c {
			h.player = nil
			h.status = Status{Online: false, LastSeenAt: time.Now(), LastError: h.status.LastError}
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
	case "progress":
		h.status.ItemID = msg.ItemID
		h.status.Position = msg.Position
		h.status.Duration = msg.Duration
		st := h.status
		h.mu.Unlock()
		h.broadcast(Event{Type: "progress", Player: &st})
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
