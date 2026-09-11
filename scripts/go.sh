#!/usr/bin/env bash
# Runs the Go toolchain: the local `go` if installed, otherwise the official
# golang Docker image with module/build caches persisted under ~/.cache.
set -euo pipefail

if command -v go >/dev/null 2>&1; then
  exec go "$@"
fi

GO_IMAGE="${GO_IMAGE:-golang:1.26-alpine}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CACHE="${GO_DOCKER_CACHE:-$HOME/.cache/central-station-go}"
mkdir -p "$CACHE/pkg" "$CACHE/build"

TTY=()
if [ -t 0 ] && [ -t 1 ]; then TTY=(-it); fi

# Extra docker flags, e.g. GO_DOCKER_ARGS="--network host" for integration tests.
EXTRA=()
if [ -n "${GO_DOCKER_ARGS:-}" ]; then read -r -a EXTRA <<<"$GO_DOCKER_ARGS"; fi

exec docker run --rm ${TTY[@]+"${TTY[@]}"} \
  -u "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -e GOMODCACHE=/go/pkg/mod \
  -e GOCACHE=/go/cache \
  -e GOFLAGS="${GOFLAGS:-}" \
  -e CGO_ENABLED=0 \
  -e TEST_DATABASE_URL="${TEST_DATABASE_URL:-}" \
  ${EXTRA[@]+"${EXTRA[@]}"} \
  -v "$CACHE/pkg:/go/pkg" \
  -v "$CACHE/build:/go/cache" \
  -v "$ROOT:/src" \
  -w /src \
  "${GO_IMAGE}" go "$@"
