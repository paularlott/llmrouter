# LLM Router Makefile

BINARY_NAME := llmrouter
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

.PHONY: all clean test lint build-all release dev

# Default target
all: build

# Build for the current platform
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) .

# Clean build artifacts
clean:
	rm -rf dist/
	rm -f $(BINARY_NAME)

# Run tests
test:
	go test -v ./...

# Run linter
lint:
	golangci-lint run

# Individual platform builds
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-amd64 .

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-arm64 .

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-darwin-amd64 .

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-darwin-arm64 .

build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-windows-amd64.exe .

build-windows-arm64:
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-windows-arm64.exe .

# Build all platforms (sequential, use Taskfile for parallel)
build-all: clean
	@mkdir -p dist
	$(MAKE) build-linux-amd64
	$(MAKE) build-linux-arm64
	$(MAKE) build-darwin-amd64
	$(MAKE) build-darwin-arm64
	$(MAKE) build-windows-amd64
	$(MAKE) build-windows-arm64

# Create release builds with checksums
release: build-all
	cd dist && sha256sum * > checksums.txt

# Run the server in development mode
dev:
	go run . server

# Display help
help:
	@echo "LLM Router Build Targets:"
	@echo "  make              - Build for current platform"
	@echo "  make build        - Build for current platform"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make test         - Run tests"
	@echo "  make lint         - Run linter"
	@echo "  make build-all    - Build for all platforms"
	@echo "  make release      - Build all platforms with checksums"
	@echo "  make dev          - Run server in development mode"
	@echo ""
	@echo "Individual platform builds:"
	@echo "  make build-linux-amd64"
	@echo "  make build-linux-arm64"
	@echo "  make build-darwin-amd64"
	@echo "  make build-darwin-arm64"
	@echo "  make build-windows-amd64"
	@echo "  make build-windows-arm64"
