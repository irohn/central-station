// Package player is the server side of the player protocol: the WebSocket hub
// that drives the headless player container and streams live state to browsers.
//
// Protocol (JSON over WebSocket, one object per message):
//
//	server -> player   {"type":"state","state":{"status":"playing","current":{...}}}
//	player -> server   {"type":"hello","name":"office-pi","version":"..."}
//	player -> server   {"type":"progress","itemId":"...","positionSeconds":12.3,"durationSeconds":240}
//	player -> server   {"type":"ended","itemId":"...","reason":"eof"}
//	player -> server   {"type":"ended","itemId":"...","reason":"error","error":"..."}
//
// The server always sends its full desired state; the player reconciles
// (load / pause / stop) so reconnects and missed messages are harmless.
package player

import (
	"time"

	"central-station/internal/queue"
)

// DesiredState is what the player should be doing right now.
type DesiredState struct {
	Status  queue.Status `json:"status"`
	Current *queue.Item  `json:"current"`
}

// ServerMessage is sent from the API to the player.
type ServerMessage struct {
	Type  string        `json:"type"` // "state"
	State *DesiredState `json:"state,omitempty"`
}

// ClientMessage is sent from the player to the API.
type ClientMessage struct {
	Type     string  `json:"type"` // hello | progress | ended
	Name     string  `json:"name,omitempty"`
	Version  string  `json:"version,omitempty"`
	ItemID   string  `json:"itemId,omitempty"`
	Position float64 `json:"positionSeconds,omitempty"`
	Duration float64 `json:"durationSeconds,omitempty"`
	Reason   string  `json:"reason,omitempty"` // eof | error
	Error    string  `json:"error,omitempty"`
}

// Status is what the API knows about the connected player.
type Status struct {
	Online      bool      `json:"online"`
	Name        string    `json:"name,omitempty"`
	Version     string    `json:"version,omitempty"`
	ConnectedAt time.Time `json:"connectedAt,omitzero"`
	LastSeenAt  time.Time `json:"lastSeenAt,omitzero"`
	ItemID      string    `json:"itemId,omitempty"`
	Position    float64   `json:"positionSeconds"`
	Duration    float64   `json:"durationSeconds"`
	LastError   string    `json:"lastError,omitempty"`
}

// Event is pushed to browser clients (spectators).
type Event struct {
	Type   string       `json:"type"` // queue | progress | player
	Queue  *queue.State `json:"queue,omitempty"`
	Player *Status      `json:"player,omitempty"`
}
