GO ?= go
BUF ?= buf
GOLANGCI_LINT ?= golangci-lint

BIN_DIR := bin
DIST_DIR := dist
INSTALL_DIR ?= $(HOME)/.local/bin
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/0xivanov/self-hosted-deployer/internal/version.Version=$(VERSION) -X github.com/0xivanov/self-hosted-deployer/internal/version.Commit=$(COMMIT) -X github.com/0xivanov/self-hosted-deployer/internal/version.BuildDate=$(BUILD_DATE)
BUILD_ENV := CGO_ENABLED=0

.PHONY: fmt test build build-arm64 build-release install-cli package release vet lint proto proto-lint proto-check clean

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

build:
	mkdir -p $(BIN_DIR)
	$(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/deployer ./cmd/deployer
	$(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/deployer-server ./cmd/deployer-server
	$(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/deployer-agent ./cmd/deployer-agent

build-arm64:
	mkdir -p $(BIN_DIR)/darwin-arm64 $(BIN_DIR)/linux-arm64
	GOOS=darwin GOARCH=arm64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/darwin-arm64/deployer ./cmd/deployer
	GOOS=linux GOARCH=arm64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/linux-arm64/deployer ./cmd/deployer
	GOOS=linux GOARCH=arm64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/linux-arm64/deployer-server ./cmd/deployer-server
	GOOS=linux GOARCH=arm64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/linux-arm64/deployer-agent ./cmd/deployer-agent

build-release: build-arm64
	mkdir -p $(BIN_DIR)/linux-amd64
	GOOS=linux GOARCH=amd64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/linux-amd64/deployer ./cmd/deployer
	GOOS=linux GOARCH=amd64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/linux-amd64/deployer-server ./cmd/deployer-server
	GOOS=linux GOARCH=amd64 $(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/linux-amd64/deployer-agent ./cmd/deployer-agent

install-cli:
	mkdir -p $(INSTALL_DIR)
	$(BUILD_ENV) $(GO) build -ldflags "$(LDFLAGS)" -o $(INSTALL_DIR)/deployer ./cmd/deployer
	@case ":$$PATH:" in *":$(INSTALL_DIR):"*) echo "deployer installed to $(INSTALL_DIR)/deployer" ;; *) echo "deployer installed to $(INSTALL_DIR)/deployer"; echo "Add $(INSTALL_DIR) to PATH to run deployer directly." ;; esac

package: build-release
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)/packages/deployer-darwin-arm64 $(DIST_DIR)/packages/deployer-linux-arm64 $(DIST_DIR)/packages/deployer-linux-amd64
	cp $(BIN_DIR)/darwin-arm64/deployer README.md $(DIST_DIR)/packages/deployer-darwin-arm64/
	cp $(BIN_DIR)/linux-arm64/deployer $(BIN_DIR)/linux-arm64/deployer-server $(BIN_DIR)/linux-arm64/deployer-agent README.md $(DIST_DIR)/packages/deployer-linux-arm64/
	cp $(BIN_DIR)/linux-amd64/deployer $(BIN_DIR)/linux-amd64/deployer-server $(BIN_DIR)/linux-amd64/deployer-agent README.md $(DIST_DIR)/packages/deployer-linux-amd64/
	cp -R deploy docs scripts $(DIST_DIR)/packages/deployer-linux-arm64/
	cp -R deploy docs scripts $(DIST_DIR)/packages/deployer-linux-amd64/
	cd $(DIST_DIR)/packages && tar -czf ../deployer-darwin-arm64.tar.gz deployer-darwin-arm64
	cd $(DIST_DIR)/packages && tar -czf ../deployer-linux-arm64.tar.gz deployer-linux-arm64
	cd $(DIST_DIR)/packages && tar -czf ../deployer-linux-amd64.tar.gz deployer-linux-amd64
	cd $(DIST_DIR) && shasum -a 256 *.tar.gz > checksums.txt

release: package

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
	rm -rf $(BIN_DIR) $(DIST_DIR)
