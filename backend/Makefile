# backend/Makefile — dev entry points for the Go monorepo half of
# skill-bridge-platform. See backend/CLAUDE.md for conventions and
# backend/README.md for how the pieces fit together.

SHELL := /bin/bash
GOBIN ?= $(shell go env GOPATH)/bin
PATH  := $(GOBIN):$(PATH)

.PHONY: setup gen migrate-up up down nuke test lint kind-up kind-load

## setup: verify the toolchain this repo assumes is present, and install
## the local protoc plugins `buf generate` shells out to (not installed
## globally by default on a fresh machine).
setup:
	@echo "==> checking required tool versions"
	@command -v go >/dev/null || (echo "go not found" && exit 1)
	@go version
	@command -v buf >/dev/null || (echo "buf not found (https://buf.build/docs/installation)" && exit 1)
	@buf --version
	@command -v golangci-lint >/dev/null || (echo "golangci-lint not found" && exit 1)
	@golangci-lint --version
	@command -v goose >/dev/null || (echo "goose not found (go install github.com/pressly/goose/v3/cmd/goose@latest)" && exit 1)
	@goose --version
	@command -v grpcurl >/dev/null || (echo "grpcurl not found" && exit 1)
	@grpcurl --version
	@command -v docker >/dev/null || (echo "docker not found" && exit 1)
	@docker --version
	@echo "==> installing local buf plugins (protoc-gen-go, protoc-gen-go-grpc)"
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "==> setup complete"

## gen: regenerate gRPC stubs from proto/ into gen/. Run this after editing
## any .proto file; gen/ is committed to git, so review the diff like any
## other source change.
gen:
	PATH="$(PATH)" buf generate

## migrate-up: apply all pending goose migrations for every service schema.
## Each service directory under migrations/ is a separate goose "workdir"
## because each service owns its own schema/role; 000_bootstrap.sql is run
## separately (manually, with an admin connection) before any of these.
migrate-up:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL must be set" && exit 1)
	goose -dir migrations/auth postgres "$$DATABASE_URL" up

## up/down/nuke: local infra lifecycle (Redis for now; Kafka/RabbitMQ land
## in later phases). nuke also drops volumes — use it when you want a truly
## clean slate, e.g. after changing a migration you already applied.
up:
	docker compose -f ../docker/docker-compose.infra.yml up -d

down:
	docker compose -f ../docker/docker-compose.infra.yml down

nuke:
	docker compose -f ../docker/docker-compose.infra.yml down -v

## test: run the Go test suite. No live DB or broker is required — see
## internal/platform/db's tests for why (config-wiring only, no round trip).
test:
	go test ./...

## lint: static analysis via golangci-lint (staticcheck/govet/errcheck/
## revive/gosec bundled — see .golangci.yml) plus buf lint on the protos.
lint:
	golangci-lint run ./...
	PATH="$(PATH)" buf lint

## kind-up / kind-load: local Kubernetes practice cluster. Real
## implementation lands in Phase 5 (see docs/DECISIONS.md) — stubbed here
## so the target exists and fails loudly rather than silently no-op'ing.
kind-up:
	@echo "not yet implemented, see Phase 5"

kind-load:
	@echo "not yet implemented, see Phase 5"
