GO ?= ./scripts/go.sh
export GOFLAGS ?= -mod=readonly
BIN := bin/api

.PHONY: help tidy build test vet fmt run up down logs docker-build clean

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n", $$1, $$2}'

tidy: ## Resolve module dependencies
	$(GO) mod tidy

fmt: ## Format code
	$(GO) fmt ./...

vet: ## Static checks
	$(GO) vet ./...

build: ## Compile the API binary to bin/api
	$(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/api

test: ## Run unit tests
	$(GO) test -count=1 ./...

test-db: ## Run tests including Postgres integration tests (starts a throwaway Postgres)
	docker rm -f cs-test-db >/dev/null 2>&1 || true
	docker run -d --name cs-test-db -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=test -p 55432:5432 postgres:17-alpine >/dev/null
	@until docker exec cs-test-db pg_isready -U test -d test >/dev/null 2>&1; do sleep 0.5; done
	GO_DOCKER_ARGS="--network host" TEST_DATABASE_URL="postgres://test:test@localhost:55432/test?sslmode=disable" $(GO) test -count=1 ./... ; \
	  status=$$?; docker rm -f cs-test-db >/dev/null; exit $$status

run: ## Run the API locally (needs a local Go toolchain; otherwise use `make up`)
	set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/api

up: ## Start the stack with docker compose
	docker compose up --build -d

down: ## Stop the stack
	docker compose down

logs: ## Tail API logs
	docker compose logs -f api

docker-build: ## Build the API image
	docker build -t central-station-api:latest .

clean:
	rm -rf bin
