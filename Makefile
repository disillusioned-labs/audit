.PHONY: run build test test-integration lint tidy sqlc sqlc-diff migrate-new migrate-up migrate-down migrate-status vuln docker-up docker-down docker-build generate-signing-key rotate-signing-key help

# Tool versions are pinned so local runs and CI can never drift. Bump here and
# in .github/workflows/ci.yml together. They are kept out of go.mod on purpose:
# sqlc's dependency tree (wazero, the tidb parser, cel-go) would otherwise be
# downloaded by the Dockerfile's `go mod download` on every image build.
GOOSE_VERSION ?= v3.27.3
SQLC_VERSION  ?= v1.30.0
VULN_VERSION  ?= v1.1.4

GOOSE := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
SQLC  := CGO_ENABLED=0 go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

# Points at the native Postgres this machine develops against. Override it to
# reach the containerised one from docker-compose (host port 5433):
#   make migrate-up DB_DSN=postgres://identity_app:devpassword@localhost:5433/identity?sslmode=disable
DB_DSN ?= postgres://identity_app:devpassword@vps:5433/identity?sslmode=disable

# Build provenance, matching the Dockerfile's ldflags so a local binary reports
# the same fields as a container one.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/disillusioned-labs/audit/internal/app.version=$(VERSION) \
	-X github.com/disillusioned-labs/audit/internal/app.commit=$(COMMIT) \
	-X github.com/disillusioned-labs/audit/internal/app.buildDate=$(BUILD_DATE)

SIGNING_KEY_IMAGE := identity-signing-key:local

run: ## Run the API locally
	go run ./cmd/api

build: ## Build the API binary into ./bin
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/api ./cmd/api

test: ## Run unit tests with race detector
	go test -race ./...

test-integration: ## Run all tests including integration ones (needs Docker)
	go test -race -tags integration ./...

lint: ## Run golangci-lint
	golangci-lint run

tidy: ## go mod tidy
	go mod tidy

sqlc: ## Regenerate type-safe query code
	$(SQLC) generate

sqlc-diff: ## Fail if generated code is stale (same check as CI)
	$(SQLC) diff

vuln: ## Scan dependencies for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@$(VULN_VERSION) ./...

migrate-new: ## Create a new migration: make migrate-new name=add_foo
	$(GOOSE) -dir db/migrations create $(name) sql

migrate-up: ## Apply migrations to local db
	$(GOOSE) -dir db/migrations postgres "$(DB_DSN)" up

migrate-down: ## Roll back one migration
	$(GOOSE) -dir db/migrations postgres "$(DB_DSN)" down

migrate-status: ## Show which migrations are applied
	$(GOOSE) -dir db/migrations postgres "$(DB_DSN)" status

docker-up: ## Start the local application stack
	docker compose up -d

docker-down: ## Stop the local application stack
	docker compose down

docker-build: ## Build the production application image
	docker build \
		--target runtime \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t identity:$(VERSION) \
		-t identity:latest \
		.

generate-signing-key: ## Generate the initial signing key
	docker build \
		--target signing-key \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(SIGNING_KEY_IMAGE) \
		.

	docker run --rm \
		--network data \
		--env-file .env.compose \
		$(SIGNING_KEY_IMAGE)

rotate-signing-key: ## Rotate the active signing key
	docker build \
		--target signing-key \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(SIGNING_KEY_IMAGE) \
		.

	docker run --rm \
		--network data \
		--env-file .env.compose \
		$(SIGNING_KEY_IMAGE) \
		--rotate


help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'