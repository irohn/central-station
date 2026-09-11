// Package playerd is the headless player agent: it keeps a WebSocket to the
// API, drives mpv over its JSON IPC socket, and reports progress and
// end-of-track events back.
package playerd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// MPVEvent is a decoded mpv IPC event.
type MPVEvent struct {
	Event           string          `json:"event"`
	Name            string          `json:"name,omitempty"`   // property-change
	Data            json.RawMessage `json:"data,omitempty"`   // property-change
	Reason          string          `json:"reason,omitempty"` // end-file: eof | stop | quit | error | redirect
	FileErr         string          `json:"file_error,omitempty"`
	PlaylistEntryID int             `json:"playlist_entry_id,omitempty"`
}

type mpvResponse struct {
	RequestID int64           `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
}

// MPV wraps a running mpv process and its IPC connection.
type MPV struct {
	cmd    *exec.Cmd
	conn   net.Conn
	logger *slog.Logger
	Events chan MPVEvent

	mu      sync.Mutex
	pending map[int64]chan mpvResponse
	nextID  atomic.Int64
	writeMu sync.Mutex
	closed  chan struct{}
}

// StartMPV launches mpv in idle mode with an IPC socket and connects to it.
// extraArgs are appended to the mpv command line (e.g. --ao=pulse).
func StartMPV(ctx context.Context, logger *slog.Logger, socketPath string, extraArgs ...string) (*MPV, error) {
	_ = os.Remove(socketPath)
	args := append([]string{
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--really-quiet",
		"--no-config",
		"--ytdl=yes",
		"--ytdl-format=bestaudio/best",
		"--input-ipc-server=" + socketPath,
		"--keep-open=no",
		"--cache=yes",
	}, extraArgs...)
	cmd := exec.CommandContext(ctx, "mpv", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mpv: %w", err)
	}

	var conn net.Conn
	var err error
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("mpv ipc socket did not appear: %w", err)
		}
		if cmd.ProcessState != nil {
			return nil, errors.New("mpv exited before creating its IPC socket")
		}
		time.Sleep(100 * time.Millisecond)
	}

	m := &MPV{cmd: cmd, conn: conn, logger: logger, Events: make(chan MPVEvent, 256), pending: map[int64]chan mpvResponse{}, closed: make(chan struct{})}
	go m.readLoop()
	go func() {
		err := cmd.Wait()
		logger.Error("mpv exited", "err", err)
		close(m.closed)
		conn.Close()
	}()

	// Observe the properties we care about.
	for i, p := range []string{"time-pos", "duration", "pause", "idle-active"} {
		if _, err := m.Command(ctx, "observe_property", i+1, p); err != nil {
			return nil, fmt.Errorf("observe %s: %w", p, err)
		}
	}
	return m, nil
}

// Done is closed when mpv exits.
func (m *MPV) Done() <-chan struct{} { return m.closed }

func (m *MPV) readLoop() {
	sc := bufio.NewScanner(m.conn)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var probe struct {
			Event     string `json:"event"`
			RequestID *int64 `json:"request_id"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Event != "" {
			var ev MPVEvent
			if json.Unmarshal(line, &ev) == nil {
				select {
				case m.Events <- ev:
				default:
					m.logger.Warn("mpv event buffer full, dropping", "event", ev.Event)
				}
			}
			continue
		}
		if probe.RequestID != nil {
			var res mpvResponse
			if json.Unmarshal(line, &res) == nil {
				m.mu.Lock()
				ch, ok := m.pending[res.RequestID]
				delete(m.pending, res.RequestID)
				m.mu.Unlock()
				if ok {
					ch <- res
				}
			}
		}
	}
	// Socket closed: fail all pending requests.
	m.mu.Lock()
	for id, ch := range m.pending {
		delete(m.pending, id)
		close(ch)
	}
	m.mu.Unlock()
}

// Command sends an IPC command and waits for its reply.
func (m *MPV) Command(ctx context.Context, args ...any) (json.RawMessage, error) {
	id := m.nextID.Add(1)
	ch := make(chan mpvResponse, 1)
	m.mu.Lock()
	m.pending[id] = ch
	m.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{"command": args, "request_id": id})
	m.writeMu.Lock()
	_, err := m.conn.Write(append(payload, '\n'))
	m.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("mpv ipc write: %w", err)
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return nil, errors.New("mpv ipc closed")
		}
		if res.Error != "success" {
			return nil, fmt.Errorf("mpv %v: %s", args[0], res.Error)
		}
		return res.Data, nil
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()
		return nil, fmt.Errorf("mpv %v: timeout", args[0])
	}
}

// LoadURL replaces whatever is playing with url.
func (m *MPV) LoadURL(ctx context.Context, url string) error {
	_, err := m.Command(ctx, "loadfile", url, "replace")
	return err
}

// Stop stops playback and clears the playlist (mpv stays idle).
func (m *MPV) Stop(ctx context.Context) error {
	_, err := m.Command(ctx, "stop")
	return err
}

// SetPause pauses or resumes.
func (m *MPV) SetPause(ctx context.Context, paused bool) error {
	_, err := m.Command(ctx, "set_property", "pause", paused)
	return err
}

// GetFloat reads a numeric property (0 if unavailable).
func (m *MPV) GetFloat(ctx context.Context, prop string) float64 {
	data, err := m.Command(ctx, "get_property", prop)
	if err != nil {
		return 0
	}
	var f float64
	_ = json.Unmarshal(data, &f)
	return f
}

// Close terminates mpv.
func (m *MPV) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = m.Command(ctx, "quit")
	select {
	case <-m.closed:
	case <-time.After(3 * time.Second):
		_ = m.cmd.Process.Kill()
	}
}
