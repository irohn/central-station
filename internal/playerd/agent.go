package playerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"central-station/internal/player"
	"central-station/internal/queue"
)

// Version is reported to the API in the hello message.
var Version = "dev"

// Agent connects to the API and reconciles mpv with the desired state.
type Agent struct {
	APIURL string // e.g. http://api:8080
	Token  string
	Name   string
	Logger *slog.Logger
	MPV    *MPV

	ProgressInterval time.Duration
	// AudioArgs are passed to the second mpv used for announcements (--ao, --audio-device).
	AudioArgs []string
	// TempDir holds downloaded announcement clips (default os.TempDir()).
	TempDir string

	// state owned by the run loop
	loadedItem *queue.Item
	paused     bool
	volume     int // last level applied to mpv; -1 until the first state
	broadcast  *player.BroadcastCommand
	bcQueue    []*player.BroadcastCommand
	bcDone     chan broadcastResult
}

type broadcastResult struct {
	id  string
	err error
}

// Run connects (and reconnects with backoff) until ctx is cancelled or mpv dies.
func (a *Agent) Run(ctx context.Context) error {
	if a.ProgressInterval == 0 {
		a.ProgressInterval = 2 * time.Second
	}
	a.volume = -1
	backoff := time.Second
	for {
		err := a.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-a.MPV.Done():
			return errors.New("mpv exited")
		default:
		}
		a.Logger.Warn("api session ended; reconnecting", "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) wsURL() string {
	u := strings.TrimRight(a.APIURL, "/") + "/api/v1/player/ws"
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return "ws://" + strings.TrimPrefix(u, "http://")
}

func (a *Agent) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, res, err := websocket.Dial(dialCtx, a.wsURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + a.Token}},
	})
	cancel()
	if err != nil {
		if res != nil {
			return errors.New("connect: HTTP " + res.Status)
		}
		return err
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	a.Logger.Info("connected to api", "url", a.wsURL())

	sctx, stop := context.WithCancel(ctx)
	defer stop()

	send := func(m player.ClientMessage) {
		wctx, cancel := context.WithTimeout(sctx, 5*time.Second)
		defer cancel()
		if err := wsjson.Write(wctx, c, m); err != nil {
			a.Logger.Debug("ws write", "err", err)
		}
	}
	send(player.ClientMessage{Type: "hello", Name: a.Name, Version: Version})

	// Reader goroutine: desired states and announcements from the server.
	states := make(chan player.DesiredState, 8)
	broadcasts := make(chan *player.BroadcastCommand, 8)
	readErr := make(chan error, 1)
	go func() {
		for {
			var m player.ServerMessage
			if err := wsjson.Read(sctx, c, &m); err != nil {
				readErr <- err
				return
			}
			switch {
			case m.Type == "state" && m.State != nil:
				states <- *m.State
			case m.Type == "broadcast" && m.Broadcast != nil:
				broadcasts <- m.Broadcast
			}
		}
	}()
	if a.bcDone == nil {
		a.bcDone = make(chan broadcastResult, 4)
	}

	ticker := time.NewTicker(a.ProgressInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sctx.Done():
			return sctx.Err()
		case err := <-readErr:
			return err
		case <-a.MPV.Done():
			return errors.New("mpv exited")
		case st := <-states:
			a.reconcile(sctx, st)
		case b := <-broadcasts:
			a.bcQueue = append(a.bcQueue, b)
			a.startBroadcast(sctx)
		case res := <-a.bcDone:
			if a.broadcast != nil && a.broadcast.ID == res.id {
				a.broadcast = nil
				// Restore the music level.
				if err := a.MPV.SetVolume(sctx, a.volume); err != nil {
					a.Logger.Warn("restore volume", "err", err)
				}
				msg := player.ClientMessage{Type: "broadcast_ended", BroadcastID: res.id, Reason: "done"}
				if res.err != nil {
					a.Logger.Error("broadcast failed", "id", res.id, "err", res.err)
					msg.Reason, msg.Error = "error", res.err.Error()
				} else {
					a.Logger.Info("broadcast finished", "id", res.id)
				}
				send(msg)
			}
			a.startBroadcast(sctx)
		case ev := <-a.MPV.Events:
			if msg, ok := a.handleMPVEvent(ev); ok {
				send(msg)
			}
		case <-ticker.C:
			if a.loadedItem != nil {
				send(player.ClientMessage{
					Type:     "progress",
					ItemID:   a.loadedItem.ID,
					Position: a.MPV.GetFloat(sctx, "time-pos"),
					Duration: a.MPV.GetFloat(sctx, "duration"),
				})
			} else {
				// Heartbeat so the server's read deadline is not hit while idle.
				send(player.ClientMessage{Type: "progress"})
			}
		}
	}
}

// reconcile makes mpv match the desired state.
func (a *Agent) reconcile(ctx context.Context, st player.DesiredState) {
	if st.Volume >= 0 && st.Volume <= 100 && st.Volume != a.volume {
		a.volume = st.Volume
		if a.broadcast == nil { // while announcing, the ducked level stays until it ends
			if err := a.MPV.SetVolume(ctx, st.Volume); err != nil {
				a.Logger.Warn("set volume", "err", err)
			} else {
				a.Logger.Info("volume", "level", st.Volume)
			}
		}
	}
	switch {
	case st.Current == nil:
		if a.loadedItem != nil {
			a.Logger.Info("stopping", "item", a.loadedItem.ID)
			if err := a.MPV.Stop(ctx); err != nil {
				a.Logger.Warn("stop", "err", err)
			}
			a.loadedItem = nil
		}
		return
	case a.loadedItem == nil || a.loadedItem.ID != st.Current.ID:
		a.Logger.Info("loading", "item", st.Current.ID, "title", st.Current.Track.Title, "url", st.Current.Track.URL)
		if err := a.MPV.LoadURL(ctx, st.Current.Track.URL); err != nil {
			a.Logger.Error("loadfile", "err", err)
		}
		a.loadedItem = st.Current
		a.paused = false
	}
	wantPause := st.Status == queue.StatusPaused
	if wantPause != a.paused {
		if err := a.MPV.SetPause(ctx, wantPause); err != nil {
			a.Logger.Warn("set pause", "err", err)
		} else {
			a.paused = wantPause
		}
	}
}

// startBroadcast plays the next queued announcement if none is playing:
// duck the music, then run a second mpv in the background.
func (a *Agent) startBroadcast(ctx context.Context) {
	if a.broadcast != nil || len(a.bcQueue) == 0 {
		return
	}
	b := a.bcQueue[0]
	a.bcQueue = a.bcQueue[1:]
	a.broadcast = b
	duck := a.volume * b.DuckPercent / 100
	if err := a.MPV.SetVolume(ctx, duck); err != nil {
		a.Logger.Warn("duck volume", "err", err)
	}
	a.Logger.Info("broadcast starting", "id", b.ID, "loops", b.Loops, "chime", b.ChimeURL != "")
	go func() {
		a.bcDone <- broadcastResult{id: b.ID, err: a.playBroadcast(ctx, b)}
	}()
}

// playBroadcast downloads the audio and plays chime + clip×loops to completion.
func (a *Agent) playBroadcast(ctx context.Context, b *player.BroadcastCommand) error {
	dir := a.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	var files []string
	cleanup := func() {
		for _, f := range files {
			os.Remove(f)
		}
	}
	defer cleanup()
	var args []string
	if b.ChimeURL != "" {
		f, err := a.download(ctx, b.ChimeURL, filepath.Join(dir, "chime-"+b.ID))
		if err != nil {
			return fmt.Errorf("chime: %w", err)
		}
		files = append(files, f)
		args = append(args, f)
	}
	clip, err := a.download(ctx, b.ClipURL, filepath.Join(dir, "clip-"+b.ID))
	if err != nil {
		return fmt.Errorf("clip: %w", err)
	}
	files = append(files, clip)
	loops := b.Loops
	if loops < 1 {
		loops = 1
	}
	for i := 0; i < loops; i++ {
		args = append(args, clip)
	}
	cmdArgs := append([]string{"--no-video", "--no-config", "--no-terminal", "--really-quiet", "--volume=100", "--keep-open=no"}, a.AudioArgs...)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "mpv", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mpv: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// download fetches an API path with the player token into base+ext.
func (a *Agent) download(ctx context.Context, path, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.APIURL, "/")+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	ext := map[string]string{"audio/mp4": ".m4a", "audio/aac": ".aac", "audio/webm": ".webm", "audio/ogg": ".ogg", "audio/opus": ".opus", "audio/wav": ".wav", "audio/mpeg": ".mp3"}[strings.SplitN(res.Header.Get("Content-Type"), ";", 2)[0]]
	if ext == "" {
		ext = ".bin"
	}
	name := base + ext
	f, err := os.Create(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(res.Body, 20<<20)); err != nil {
		return "", err
	}
	return name, nil
}

// handleMPVEvent converts mpv events into protocol messages.
func (a *Agent) handleMPVEvent(ev MPVEvent) (player.ClientMessage, bool) {
	switch ev.Event {
	case "end-file":
		if a.loadedItem == nil {
			return player.ClientMessage{}, false
		}
		switch ev.Reason {
		case "eof":
			a.Logger.Info("finished", "item", a.loadedItem.ID)
			id := a.loadedItem.ID
			a.loadedItem = nil
			return player.ClientMessage{Type: "ended", ItemID: id, Reason: "eof"}, true
		case "error":
			msg := ev.FileErr
			if msg == "" {
				msg = "mpv could not play this item (is yt-dlp up to date?)"
			}
			a.Logger.Error("playback error", "item", a.loadedItem.ID, "err", msg)
			id := a.loadedItem.ID
			a.loadedItem = nil
			return player.ClientMessage{Type: "ended", ItemID: id, Reason: "error", Error: msg}, true
		}
		// "stop" / "redirect" / "quit": caused by us, ignore.
	case "property-change":
		if ev.Name == "pause" {
			var p bool
			if json.Unmarshal(ev.Data, &p) == nil {
				a.paused = p
			}
		}
	}
	return player.ClientMessage{}, false
}
