# LLM Router Makefile — mirrors the Taskfile for users who prefer make.
# The Taskfile has additional features (macOS .app packaging, icon generation).

BINARY_NAME := llmrouter
VERSION := $(shell go run ./tools/getversion)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags="-s -w -X github.com/paularlott/llmrouter/build.BuildDate=$(BUILD_DATE)"
WINDOWS_LDFLAGS := -ldflags="-s -w -H windowsgui -X github.com/paularlott/llmrouter/build.BuildDate=$(BUILD_DATE)"

REGISTRY ?= paularlott
HUB ?= registry-1.docker.io/
PLATFORM_ARG := --platform linux/amd64,linux/arm64

.PHONY: all build clean test lint assets \
	build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 \
	build-windows-amd64 build-windows-arm64 build-all create-zips release \
	homebrew-formula run-server run-desktop container container-local tag help

all: build

assets:
	cd web && npm run build

build: assets
	go build $(LDFLAGS) -o $(BINARY_NAME) .

clean:
	rm -rf dist/ $(BINARY_NAME) web/dist/

test:
	go test -v ./...

lint:
	golangci-lint run

# ── Platform builds (CGO_ENABLED=0, cross-compile from any host) ──

build-linux-amd64: assets
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-amd64 .

build-linux-arm64: assets
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-linux-arm64 .

build-darwin-amd64: assets
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-amd64 .

build-darwin-arm64: assets
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY_NAME)-darwin-arm64 .

build-windows-amd64: assets
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(WINDOWS_LDFLAGS) -o dist/$(BINARY_NAME)-windows-amd64.exe .

build-windows-arm64: assets
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build $(WINDOWS_LDFLAGS) -o dist/$(BINARY_NAME)-windows-arm64.exe .

build-all: clean
	mkdir -p dist
	$(MAKE) build-linux-amd64
	$(MAKE) build-linux-arm64
	$(MAKE) build-darwin-amd64
	$(MAKE) build-darwin-arm64
	$(MAKE) build-windows-amd64
	$(MAKE) build-windows-arm64
	$(MAKE) create-zips

create-zips:
	cd dist && cp $(BINARY_NAME)-linux-amd64 $(BINARY_NAME) && zip $(BINARY_NAME)-linux-amd64.zip $(BINARY_NAME) && rm $(BINARY_NAME)
	cd dist && cp $(BINARY_NAME)-linux-arm64 $(BINARY_NAME) && zip $(BINARY_NAME)-linux-arm64.zip $(BINARY_NAME) && rm $(BINARY_NAME)
	cd dist && cp $(BINARY_NAME)-darwin-amd64 $(BINARY_NAME) && zip $(BINARY_NAME)-darwin-amd64.zip $(BINARY_NAME) && rm $(BINARY_NAME)
	cd dist && cp $(BINARY_NAME)-darwin-arm64 $(BINARY_NAME) && zip $(BINARY_NAME)-darwin-arm64.zip $(BINARY_NAME) && rm $(BINARY_NAME)
	cd dist && cp $(BINARY_NAME)-windows-amd64.exe $(BINARY_NAME).exe && zip $(BINARY_NAME)-windows-amd64.zip $(BINARY_NAME).exe && rm $(BINARY_NAME).exe
	cd dist && cp $(BINARY_NAME)-windows-arm64.exe $(BINARY_NAME).exe && zip $(BINARY_NAME)-windows-arm64.zip $(BINARY_NAME).exe && rm $(BINARY_NAME).exe

release: build-all
	@if git tag -l v$(VERSION) | grep -q v$(VERSION); then \
		echo "Tag v$(VERSION) already exists"; \
	else \
		echo "Creating tag v$(VERSION)"; \
		git tag -a v$(VERSION) -m "Release $(VERSION)"; \
		git push origin v$(VERSION); \
	fi
	gh release create v$(VERSION) -t "Release $(VERSION)" -n "LLM Router $(VERSION)" dist/*.zip
	$(MAKE) homebrew-formula

homebrew-formula:
	go run ./scripts/homebrew-formula/ -out ../homebrew-tap

run-server: assets
	go run . server --personas-dir ./examples/personas --commands-dir ./examples/commands --resources-dir ./examples/resources --prompts-dir ./examples/prompts --routes-dir ./examples/routers

run-desktop: assets
	go run . --personas-dir ./examples/personas --commands-dir ./examples/commands --resources-dir ./examples/resources --prompts-dir ./examples/prompts --routes-dir ./examples/routers

container:
	@echo "Building for platforms: $(PLATFORM_ARG)"
	docker buildx build $(PLATFORM_ARG) --tag $(REGISTRY)/llmrouter:$(VERSION) --tag $(REGISTRY)/llmrouter:latest --build-arg VERSION=$(VERSION) --build-arg DOCKER_HUB=$(HUB) --push .

container-local:
	docker build --tag $(REGISTRY)/llmrouter:$(VERSION) --tag $(REGISTRY)/llmrouter:latest --build-arg VERSION=$(VERSION) --build-arg DOCKER_HUB=$(HUB) .

tag:
	@git tag -l v$(VERSION) | grep -q v$(VERSION) && echo "Tag v$(VERSION) exists" || \
		(git tag -a v$(VERSION) -m "Release $(VERSION)" && git push origin v$(VERSION))

help:
	@echo "LLM Router Build Targets:"
	@echo "  make              - Build for current platform"
	@echo "  make build-all    - Build all platforms with ZIP archives"
	@echo "  make release      - Build, create GitHub release, update Homebrew"
	@echo "  make run-server   - Run server in development mode"
	@echo "  make run-desktop  - Run desktop app in development mode"
	@echo "  make clean        - Remove build artifacts"
	@echo "  make test         - Run tests"
	@echo "  make container    - Build and push multi-arch container image"
	@echo ""
	@echo "Individual platform builds:"
	@echo "  make build-linux-amd64    make build-linux-arm64"
	@echo "  make build-darwin-amd64   make build-darwin-arm64"
	@echo "  make build-windows-amd64  make build-windows-arm64"
