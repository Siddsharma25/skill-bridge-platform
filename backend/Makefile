# backend/Makefile — dev entry points for the Go monorepo half of
# skill-bridge-platform. See backend/CLAUDE.md for conventions and
# backend/README.md for how the pieces fit together.

SHELL := /bin/bash
GOBIN ?= $(shell go env GOPATH)/bin
PATH  := $(GOBIN):$(PATH)

.PHONY: setup gen migrate-up up down nuke up-full down-full nuke-full test lint kind-up kind-load

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
## Against real Supabase, each service should run this with its own scoped
## DATABASE_URL (its own role); against a single throwaway local Postgres
## (e.g. for the Phase 1b end-to-end smoke test) one admin DATABASE_URL
## covering all schemas is fine to run all four in sequence with.
##
## -table is required on every invocation: goose's default tracking table
## (goose_db_version, unqualified -> the `public` schema) is shared by
## every -dir target against the same database/search_path, so the
## second service to migrate sees the first service's version number and
## silently no-ops ("no migrations to run") even though its own tables
## were never created — discovered running this end-to-end in Phase 1b.
## Each service's tracking table lives in its own schema instead, which
## also means it exists under a role that can actually write it (a
## scoped role has no grants on `public`).
migrate-up:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL must be set" && exit 1)
	goose -dir migrations/auth -table auth.goose_db_version postgres "$$DATABASE_URL" up
	goose -dir migrations/users -table users.goose_db_version postgres "$$DATABASE_URL" up
	goose -dir migrations/skills -table skills.goose_db_version postgres "$$DATABASE_URL" up
	goose -dir migrations/jobs -table jobs.goose_db_version postgres "$$DATABASE_URL" up
	goose -dir migrations/notifications -table notifications.goose_db_version postgres "$$DATABASE_URL" up

## up/down/nuke: local infra lifecycle (Redis + Kafka + RabbitMQ as of
## Phase 3). nuke also drops volumes — use it when you want a truly clean
## slate, e.g. after changing a migration you already applied.
up:
	docker compose -f ../docker/docker-compose.infra.yml up -d

down:
	docker compose -f ../docker/docker-compose.infra.yml down

nuke:
	docker compose -f ../docker/docker-compose.infra.yml down -v

## up-full/down-full/nuke-full: Phase 4's full stack — infra (Redis, Kafka,
## RabbitMQ) PLUS the 6 app services (auth/skills/users/jobs/api-gateway,
## notification-service), built from source via docker/docker-compose.yml.
## Distinct from up/down/nuke above on purpose: local dev without Docker
## (`go run ./cmd/<service>` directly against `make up`'s infra-only stack)
## is still the faster, more common inner-loop workflow and shouldn't
## require a Docker image rebuild on every iteration — these targets are
## for proving the whole containerized stack works from a clean checkout,
## not for everyday development. Requires docker/.env (copy
## docker/.env.example and fill in real values — see docs/DECISIONS.md's
## Phase 4 notes) via --env-file, since each of the 5 database-backed
## services needs its own DATABASE_URL and Compose's env-file lookup
## doesn't default to docker/ when invoked from here.
up-full:
	docker compose --env-file ../docker/.env -f ../docker/docker-compose.infra.yml -f ../docker/docker-compose.yml up -d --build

down-full:
	docker compose --env-file ../docker/.env -f ../docker/docker-compose.infra.yml -f ../docker/docker-compose.yml down

nuke-full:
	docker compose --env-file ../docker/.env -f ../docker/docker-compose.infra.yml -f ../docker/docker-compose.yml down -v

## test: run the Go test suite. No live DB or broker is required — see
## internal/platform/db's tests for why (config-wiring only, no round trip).
test:
	go test ./...

## lint: static analysis via golangci-lint (staticcheck/govet/errcheck/
## revive/gosec bundled — see .golangci.yml) plus buf lint on the protos.
lint:
	golangci-lint run ./...
	PATH="$(PATH)" buf lint

## kind-up: create the local Kubernetes practice cluster and install both
## operators (Strimzi for Kafka, the official RabbitMQ Cluster Operator),
## waiting for each to be ready. Does NOT apply the Kafka/RabbitMQ custom
## resources, the throwaway Postgres, or the app manifests — see
## docs/DECISIONS.md's Phase 5 implementation notes for the full apply
## order (operators must be Ready before their CRDs' custom resources can
## be created) and for the exact commands this wraps.
kind-up:
	@command -v kind >/dev/null || (echo "kind not found" && exit 1)
	kind create cluster --name skill-bridge --config ../k8s/kind-cluster-config.yaml
	kubectl create namespace kafka --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f 'https://strimzi.io/install/latest?namespace=kafka' -n kafka
	kubectl apply -f "https://github.com/rabbitmq/cluster-operator/releases/latest/download/cluster-operator.yml"
	@echo "==> waiting for Strimzi cluster operator to be ready"
	kubectl wait deployment/strimzi-cluster-operator -n kafka --for=condition=Available --timeout=180s
	@echo "==> waiting for RabbitMQ cluster operator to be ready"
	kubectl wait deployment/rabbitmq-cluster-operator -n rabbitmq-system --for=condition=Available --timeout=180s
	@echo "==> operators ready. Next: apply k8s/strimzi/kafka-cluster.yaml and"
	@echo "    k8s/rabbitmq/rabbitmq-cluster.yaml (with a real password substituted"
	@echo "    for RabbitMQ), the throwaway verification Postgres, then"
	@echo "    'kubectl apply -k ../k8s/overlays/kind' — see docs/DECISIONS.md."

## kind-load: build every service's image and load it into the kind
## cluster's node (kind's nodes are containers with their own isolated
## image store — a plain `docker build` alone is invisible to them; see
## https://kind.sigs.k8s.io/docs/user/quick-start/#loading-an-image-into-your-cluster).
## Reuses the existing Dockerfile.<service> files and
## notification-service/Dockerfile as-is.
kind-load:
	@command -v kind >/dev/null || (echo "kind not found" && exit 1)
	docker build -t skill-bridge/auth-service:kind -f Dockerfile.auth-service .
	docker build -t skill-bridge/users-service:kind -f Dockerfile.users-service .
	docker build -t skill-bridge/skills-service:kind -f Dockerfile.skills-service .
	docker build -t skill-bridge/jobs-service:kind -f Dockerfile.jobs-service .
	docker build -t skill-bridge/api-gateway:kind -f Dockerfile.api-gateway .
	docker build -t skill-bridge/notification-service:kind notification-service
	kind load docker-image skill-bridge/auth-service:kind --name skill-bridge
	kind load docker-image skill-bridge/users-service:kind --name skill-bridge
	kind load docker-image skill-bridge/skills-service:kind --name skill-bridge
	kind load docker-image skill-bridge/jobs-service:kind --name skill-bridge
	kind load docker-image skill-bridge/api-gateway:kind --name skill-bridge
	kind load docker-image skill-bridge/notification-service:kind --name skill-bridge
