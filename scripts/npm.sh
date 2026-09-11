#!/usr/bin/env bash
# Runs npm for the web app: local npm if installed, otherwise the node Docker image.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT/web"
if command -v npm >/dev/null 2>&1; then
  exec npm "$@"
fi
TTY=(); if [ -t 0 ] && [ -t 1 ]; then TTY=(-it); fi
mkdir -p "$HOME/.cache/central-station-npm"
PORT=(); case "${1:-}${2:-}" in *dev*|*preview*) PORT=(-p 3000:3000);; esac
exec docker run --rm "${TTY[@]}" -u "$(id -u):$(id -g)" -e HOME=/tmp -e npm_config_cache=/npm-cache \
  -v "$HOME/.cache/central-station-npm:/npm-cache" -v "$ROOT/web:/app" -w /app \
  "${PORT[@]}" "${NODE_IMAGE:-node:22-alpine}" npm "$@"
