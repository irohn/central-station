package playerd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
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

	// state owned by the run loop
	loadedItem *queue.Item
	paused     bool
}

// Run connects (and reconnects with backoff) until ctx is cancelled or mpv dies.
func (a *Agent) Run(ctx context.Context) error {
	if a.ProgressInterval == 0 {
		a.ProgressInterval = 2 * time.Second
	}
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

	// Reader goroutine: desired states from the server.
	states := make(chan player.DesiredState, 8)
	readErr := make(chan error, 1)
	go func() {
		for {
			var m player.ServerMessage
			if err := wsjson.Read(sctx, c, &m); err != nil {
				readErr <- err
				return
			}
			if m.Type == "state" && m.State != nil {
				states <- *m.State
			}
		}
	}()

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
