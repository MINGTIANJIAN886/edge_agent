APP_NAME ?= agent
BUILD_DIR ?= build
COMMIT_HASH ?= $(shell git log --format="%h" -1 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS ?= -ldflags="-X main.Version=$(COMMIT_HASH) -X main.BuildTime=$(BUILD_TIME) -s -w"

.PHONY: all clean deps build build-armv7l build-aarch64

all: clean deps build-armv7l build-aarch64

deps:
	go mod download

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-amd64 ./cmd/agent

build-armv7l:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-armv7l ./cmd/agent

build-aarch64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(APP_NAME)-aarch64 ./cmd/agent

build-all: build build-armv7l build-aarch64

strip:
	strip $(BUILD_DIR)/* 2>/dev/null; true

compress: build-all strip
	upx --best $(BUILD_DIR)/* 2>/dev/null; true

clean:
	rm -rf $(BUILD_DIR)

release: clean deps build-all compress
	@echo "---"
	@echo "Release binaries in $(BUILD_DIR)/:"
	@ls -lh $(BUILD_DIR)/
	@echo "---"
	@cd $(BUILD_DIR) && sha256sum * > SHA256SUMS
