GO ?= go
GOLANGCI_LINT ?= golangci-lint
VERSION ?= dev
BUILD_TIME ?= unknown
COMMIT ?= unknown
EXE := $(shell $(GO) env GOEXE)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.commit=$(COMMIT)

.PHONY: help lint test test-integration test-authz-benchmark run-api run-worker migrate-up migrate-down migrate-version seed permissions-export build docker
.DEFAULT_GOAL := help

help:
	@echo "lint test test-integration run-api run-worker migrate-up migrate-down migrate-version seed permissions-export build docker"

lint:
	$(GOLANGCI_LINT) run

test:
	$(GO) test ./...

test-integration:
	$(GO) test -count=1 -v ./internal/kernel/db ./internal/bootstrap ./cmd/cli -run Integration

test-authz-benchmark:
	$(GO) test -count=1 -tags authzbench -v ./internal/bootstrap -run '^TestAuthzIntegration2000UserBenchmark$$'

seed:
	$(GO) run ./cmd/cli seed

permissions-export:
	$(GO) run ./cmd/cli permissions export

run-api:
	$(GO) run ./cmd/api

run-worker:
	$(GO) run ./cmd/worker

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/api$(EXE) ./cmd/api
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/worker$(EXE) ./cmd/worker
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/cli$(EXE) ./cmd/cli

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg BUILD_TIME=$(BUILD_TIME) --build-arg COMMIT=$(COMMIT) -t commercehub:$(VERSION) .

migrate-up:
	$(GO) run ./cmd/cli migrate up

migrate-down:
	$(GO) run ./cmd/cli migrate down

migrate-version:
	$(GO) run ./cmd/cli migrate version
