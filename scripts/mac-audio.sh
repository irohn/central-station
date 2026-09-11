#!/usr/bin/env bash
# Run PulseAudio on macOS and expose it to the player container over TCP.
#
# Docker Desktop cannot pass the Mac's sound hardware or a unix socket into a
# container, so the player connects to this daemon at host.docker.internal:4713
# (see docker-compose.mac.yml). Output goes to the Mac's current CoreAudio device.
#
# Usage: ./scripts/mac-audio.sh [start|stop|status|logs]   (default: start)
set -euo pipefail

PORT="${PULSE_TCP_PORT:-4713}"
LOG="${TMPDIR:-/tmp}/central-station-pulseaudio.log"

if ! command -v pulseaudio >/dev/null 2>&1; then
  echo "pulseaudio not found. Install it with: brew install pulseaudio" >&2
  exit 1
fi

case "${1:-start}" in
  start)
    if pulseaudio --check 2>/dev/null; then
      echo "PulseAudio is already running; run '$0 stop' first to restart it with TCP enabled."
      exit 0
    fi
    # --daemonize fails on macOS ("Daemon startup failed"), so background it here.
    # Only listen on loopback: Docker Desktop delivers container traffic to the
    # host from 127.0.0.1, so nothing else on the LAN can reach the daemon.
    nohup pulseaudio --daemonize=no --exit-idle-time=-1 \
      --load="module-native-protocol-tcp port=${PORT} listen=127.0.0.1 auth-anonymous=1" \
      >"$LOG" 2>&1 &
    for _ in $(seq 1 20); do
      pulseaudio --check 2>/dev/null && break
      sleep 0.25
    done
    if ! pulseaudio --check 2>/dev/null; then
      echo "PulseAudio failed to start; see $LOG" >&2; tail -20 "$LOG" >&2; exit 1
    fi
    # Make sure a CoreAudio sink exists (Homebrew's default.pa normally loads it).
    if ! pactl list short sinks | grep -q .; then
      pactl load-module module-coreaudio-detect >/dev/null
    fi
    echo "PulseAudio listening on 127.0.0.1:${PORT} (log: $LOG). Sinks:"
    pactl list short sinks
    ;;
  stop)
    pulseaudio --kill && echo "PulseAudio stopped."
    ;;
  status)
    if pulseaudio --check 2>/dev/null; then
      echo "PulseAudio is running."; pactl list short sinks
    else
      echo "PulseAudio is not running."; exit 1
    fi
    ;;
  logs)
    tail -f "$LOG"
    ;;
  *)
    echo "usage: $0 [start|stop|status|logs]" >&2; exit 2 ;;
esac
