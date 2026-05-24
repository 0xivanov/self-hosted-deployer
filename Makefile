GO ?= go
BUF ?= buf
GOLANGCI_LINT ?= golangci-lint

BIN_DIR := bin
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/0xivanov/self-hosted-deployer/internal/version.Version=$(VERSION) -X github.com/0xivanov/self-hosted-deployer/internal/version.Commit=$(COMMIT) -X github.com/0xivanov/self-hosted-deployer/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: fmt test build vet lint proto proto-lint proto-check clean

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/deployer ./cmd/deployer
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/deployer-server ./cmd/deployer-server
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/deployer-agent ./cmd/deployer-agent

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

proto:
	$(BUF) generate

proto-lint:
	$(BUF) lint

proto-check: proto
	git diff --exit-code -- proto internal/proto

clean:
	rm -rf $(BIN_DIR)
