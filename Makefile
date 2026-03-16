SHELL := /bin/bash
.DEFAULT_GOAL := help

# ─── Variables ────────────────────────────────────────────────────────────────
MODULE      := github.com/your-org/openpay-smart-service
PROTO_DIR   := proto
GEN_DIR     := gen
MIGRATE_DIR := migrations
DB_DSN      ?= postgres://openpay:openpay@localhost:5432/openpay_smart?sslmode=disable

GO          := go
GOOSE       := goose
PROTOC      := protoc
BUF         := buf

# ─── Help ─────────────────────────────────────────────────────────────────────
.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ─── Dev setup ────────────────────────────────────────────────────────────────
.PHONY: setup
setup: ## Install all dev tools (protoc plugins, goose, buf, etc.)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
	go install github.com/pressly/goose/v3/cmd/goose@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	@echo "✓ Dev tools installed"

# ─── Proto codegen ────────────────────────────────────────────────────────────
.PHONY: proto
proto: ## Generate Go + gateway stubs from .proto files
	mkdir -p $(GEN_DIR)
	$(PROTOC) \
		--proto_path=$(PROTO_DIR) \
		--proto_path=$(shell go env GOPATH)/pkg/mod/github.com/grpc-ecosystem/grpc-gateway/v2@*/third_party/googleapis \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=$(GEN_DIR) --grpc-gateway_opt=paths=source_relative \
		$(shell find $(PROTO_DIR) -name '*.proto')
	@echo "✓ Proto stubs generated in $(GEN_DIR)/"

# ─── Build ────────────────────────────────────────────────────────────────────
.PHONY: build
build: ## Build server and worker binaries
	$(GO) build -o bin/server ./cmd/server
	$(GO) build -o bin/worker ./cmd/worker
	@echo "✓ Binaries written to bin/"

.PHONY: build-docker
build-docker: ## Build Docker images for server + worker
	docker build --target server -t openpay-smart-server:dev .
	docker build --target worker -t openpay-smart-worker:dev .

# ─── Run ──────────────────────────────────────────────────────────────────────
.PHONY: run-server
run-server: ## Run gRPC server locally
	$(GO) run ./cmd/server --config config.yaml

.PHONY: run-worker
run-worker: ## Run Kafka worker locally
	$(GO) run ./cmd/worker --config config.yaml

.PHONY: up
up: ## Start all dependencies via Docker Compose
	docker compose up -d postgres redis zookeeper kafka
	@echo "Waiting for services to be healthy..."
	@sleep 5

.PHONY: up-tools
up-tools: ## Start all dependencies + observability tools (Jaeger, Kafka UI)
	docker compose --profile tools up -d

.PHONY: down
down: ## Stop and remove all containers
	docker compose down

.PHONY: down-v
down-v: ## Stop containers and remove volumes
	docker compose down -v

# ─── Migrations ───────────────────────────────────────────────────────────────
.PHONY: migrate-up
migrate-up: ## Apply all pending database migrations
	$(GOOSE) -dir $(MIGRATE_DIR) postgres "$(DB_DSN)" up

.PHONY: migrate-down
migrate-down: ## Roll back the last migration
	$(GOOSE) -dir $(MIGRATE_DIR) postgres "$(DB_DSN)" down

.PHONY: migrate-status
migrate-status: ## Show migration status
	$(GOOSE) -dir $(MIGRATE_DIR) postgres "$(DB_DSN)" status

.PHONY: migrate-create
migrate-create: ## Create a new migration (usage: make migrate-create NAME=add_foo)
	$(GOOSE) -dir $(MIGRATE_DIR) create $(NAME) sql

# ─── Test ─────────────────────────────────────────────────────────────────────
.PHONY: test
test: ## Run unit tests
	$(GO) test -race -count=1 ./...

.PHONY: test-int
test-int: ## Run integration tests (requires Docker Compose up)
	$(GO) test -race -count=1 -tags integration ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ─── Lint & format ────────────────────────────────────────────────────────────
.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format all Go source files
	$(GO) fmt ./...
	goimports -w .

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

# ─── Codegen ──────────────────────────────────────────────────────────────────
.PHONY: sqlc
sqlc: ## Generate type-safe Go code from SQL queries (sqlc)
	sqlc generate

# ─── Crypto helpers ───────────────────────────────────────────────────────────
.PHONY: gen-aes-key
gen-aes-key: ## Generate a random AES-256 key for OPENPAY_ENCRYPTION_AES_KEY_HEX
	@openssl rand -hex 32

.PHONY: gen-api-key
gen-api-key: ## Generate a sample tenant API key
	@echo "opk_live_$$(openssl rand -hex 24)"

# ─── Clean ────────────────────────────────────────────────────────────────────
.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
