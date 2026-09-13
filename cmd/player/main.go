// Command player is the headless office player. It connects to the API with
// PLAYER_TOKEN and plays whatever the queue says through mpv.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"central-station/internal/playerd"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	apiURL := envOr("API_URL", "http://api:8080")
	token := os.Getenv("PLAYER_TOKEN")
	name := envOr("PLAYER_NAME", hostname())
	if token == "" {
		logger.Error("PLAYER_TOKEN is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var mpvArgs []string
	if ao := os.Getenv("MPV_AO"); ao != "" {
		mpvArgs = append(mpvArgs, "--ao="+ao)
	}
	if dev := os.Getenv("MPV_AUDIO_DEVICE"); dev != "" {
		mpvArgs = append(mpvArgs, "--audio-device="+dev)
	}
	if extra := strings.TrimSpace(os.Getenv("MPV_EXTRA_ARGS")); extra != "" {
		mpvArgs = append(mpvArgs, strings.Fields(extra)...)
	}

	socketPath := envOr("MPV_SOCKET", "/tmp/mpv.sock")
	startMPV := func(ctx context.Context) (*playerd.MPV, error) {
		return playerd.StartMPV(ctx, logger, socketPath, mpvArgs...)
	}
	mpv, err := startMPV(ctx)
	if err != nil {
		logger.Error("start mpv", "err", err)
		os.Exit(1)
	}
	logger.Info("mpv ready", "name", name, "api", apiURL, "args", mpvArgs)

	agent := &playerd.Agent{APIURL: apiURL, Token: token, Name: name, Logger: logger, MPV: mpv, StartMPV: startMPV, ProgressInterval: 2 * time.Second, AudioArgs: mpvArgs}
	defer func() { agent.MPV.Close() }()
	if err := agent.Run(ctx); err != nil {
		logger.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "player"
	}
	return h
}
