// Package player is the server side of the player protocol: the WebSocket hub
// that drives the headless player container and streams live state to browsers.
//
// Protocol (JSON over WebSocket, one object per message):
//
//	server -> player   {"type":"state","state":{"status":"playing","current":{...},"volume":80}}
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
	// Volume is the output level 0-100 (mpv software volume; 100 = the
	// host's mixer level unchanged).
	Volume int `json:"volume"`
}

// ServerMessage is sent from the API to the player.
type ServerMessage struct {
	Type      string            `json:"type"` // state | broadcast
	State     *DesiredState     `json:"state,omitempty"`
	Broadcast *BroadcastCommand `json:"broadcast,omitempty"`
}

// BroadcastCommand asks the player to play a clip over the music: an optional
// chime, then the clip Loops times, while the music is ducked to DuckPercent
// of its volume. URLs are API paths the player fetches with its token.
type BroadcastCommand struct {
	ID          string `json:"id"`
	ClipURL     string `json:"clipUrl"`
	ChimeURL    string `json:"chimeUrl,omitempty"`
	Loops       int    `json:"loops"`
	DuckPercent int    `json:"duckPercent"`
}

// ClientMessage is sent from the player to the API.
type ClientMessage struct {
	Type        string  `json:"type"` // hello | progress | ended | broadcast_ended
	Name        string  `json:"name,omitempty"`
	Version     string  `json:"version,omitempty"`
	ItemID      string  `json:"itemId,omitempty"`
	BroadcastID string  `json:"broadcastId,omitempty"`
	Position    float64 `json:"positionSeconds,omitempty"`
	Duration    float64 `json:"durationSeconds,omitempty"`
	Reason      string  `json:"reason,omitempty"` // eof | error
	Error       string  `json:"error,omitempty"`
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
	// Volume is the level the player is asked to use (0-100).
	Volume int `json:"volume"`
	// Broadcast is the announcement playing now, if any.
	Broadcast *BroadcastStatus `json:"broadcast,omitempty"`
	// BroadcastsQueued counts announcements waiting behind it.
	BroadcastsQueued int `json:"broadcastsQueued"`
}

// BroadcastStatus describes an announcement in flight.
type BroadcastStatus struct {
	ID        string    `json:"id"`
	ClipID    string    `json:"clipId"`
	ClipName  string    `json:"clipName"`
	Loops     int       `json:"loops"`
	ByID      string    `json:"byId"`
	ByName    string    `json:"byName"`
	StartedAt time.Time `json:"startedAt"`
}

// Event is pushed to browser clients (spectators).
type Event struct {
	Type   string       `json:"type"` // queue | progress | player | zones
	Queue  *queue.State `json:"queue,omitempty"`
	Player *Status      `json:"player,omitempty"`
	// Zones carries the summary of every zone (type "zones"); its element
	// type is defined by the zones package.
	Zones any `json:"zones,omitempty"`
}
