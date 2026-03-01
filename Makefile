# LLM Router Makefile

BINARY_NAME := llmrouter
VERSION := $(shell go run ./tools/getversion)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags="-s -w -X github.com/paularlott/llmrouter/build.BuildDate=$(BUILD_DATE)"

# Container build settings
REGISTRY ?= paularlott
HUB ?= registry-1.docker.io/
PLATFORM_ARG := --platform linux/amd64,linux/arm64

# Detect container runtime (docker preferred over podman)
ifeq ($(BUILD_WITH),)
	ifneq ($(shell command -v docker 2>/dev/null),)
		BUILD_WITH := docker
	else ifneq ($(shell command -v podman 2>/dev/null),)
		BUILD_WITH := podman
	else
		BUILD_WITH := none
	endif
endif

.PHONY: all clean test lint assets build build-all release dev container container-local tag

# Build frontend assets
assets:
	cd web && npm run build

# Default target
all: build

# Build for the current platform
build: assets
	go build $(LDFLAGS) -o $(BINARY_NAME) .

# Clean build artifacts
clean:
	rm -rf dist/
	rm -f $(BINARY_NAME)
	rm -rf web/dist/

# Run tests
test:
	go test -v ./...

# Run linter
lint:
	golangci-lint run

# Individual platform builds (assets already built by build-all)
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 .

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-arm64 .

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 .

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 .

build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe .

build-windows-arm64:
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-windows-arm64.exe .

# Build all platforms (sequential, use Taskfile for parallel)
build-all: clean assets
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
run-server: assets
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
	@echo "  make container    - Build and push multi-arch container image"
	@echo "  make container-local - Build container image locally (no push)"
	@echo "  make tag          - Tag version and push to GitHub"
	@echo ""
	@echo "Container settings (override with environment variables):"
	@echo "  REGISTRY=$(REGISTRY)"
	@echo "  BUILD_WITH=$(BUILD_WITH)"
	@echo ""
	@echo "Individual platform builds:"
	@echo "  make build-linux-amd64"
	@echo "  make build-linux-arm64"
	@echo "  make build-darwin-amd64"
	@echo "  make build-darwin-arm64"
	@echo "  make build-windows-amd64"
	@echo "  make build-windows-arm64"

# Build and push multi-arch container image
container:
	@echo "Detected container runtime: $(BUILD_WITH)"
	@echo "Building for platforms: $(PLATFORM_ARG)"
	@if [ "$(BUILD_WITH)" = "podman" ]; then \
		podman manifest create $(REGISTRY)/llmrouter:$(VERSION); \
		podman build $(PLATFORM_ARG) --manifest $(REGISTRY)/llmrouter:$(VERSION) --build-arg VERSION=$(VERSION) --build-arg DOCKER_HUB=$(HUB) .; \
		podman tag $(REGISTRY)/llmrouter:$(VERSION) $(REGISTRY)/llmrouter:latest; \
		podman manifest push $(REGISTRY)/llmrouter:$(VERSION); \
		podman manifest push $(REGISTRY)/llmrouter:latest; \
	elif [ "$(BUILD_WITH)" = "docker" ]; then \
		docker buildx build $(PLATFORM_ARG) --tag $(REGISTRY)/llmrouter:$(VERSION) --tag $(REGISTRY)/llmrouter:latest --build-arg VERSION=$(VERSION) --build-arg DOCKER_HUB=$(HUB) --push .; \
	else \
		echo "Error: No container runtime detected. Install docker or podman."; \
		exit 1; \
	fi

# Build container image locally (no push, current platform only)
container-local:
	@echo "Detected container runtime: $(BUILD_WITH)"
	@echo "Building for local platform only"
	@if [ "$(BUILD_WITH)" = "podman" ]; then \
		podman build --tag $(REGISTRY)/llmrouter:$(VERSION) --tag $(REGISTRY)/llmrouter:latest --build-arg VERSION=$(VERSION) --build-arg DOCKER_HUB=$(HUB) .; \
	elif [ "$(BUILD_WITH)" = "docker" ]; then \
		docker build --tag $(REGISTRY)/llmrouter:$(VERSION) --tag $(REGISTRY)/llmrouter:latest --build-arg VERSION=$(VERSION) --build-arg DOCKER_HUB=$(HUB) .; \
	else \
		echo "Error: No container runtime detected. Install docker or podman."; \
		exit 1; \
	fi

# Tag the current code with version and push to GitHub
tag:
	@if git tag -l v$(VERSION) | grep -q v$(VERSION); then \
		echo "Tag v$(VERSION) already exists"; \
	else \
		echo "Creating tag v$(VERSION)"; \
		git tag -a v$(VERSION) -m "Release $(VERSION)"; \
		git push origin v$(VERSION); \
	fi
