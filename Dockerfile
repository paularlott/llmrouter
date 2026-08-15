ARG DOCKER_HUB

FROM --platform=${BUILDPLATFORM} ${DOCKER_HUB}library/golang:1.26.5-alpine AS builder

# Set build arguments
ARG TARGETPLATFORM
ARG TARGETARCH

RUN apk update \
  && apk add --no-cache bash nodejs npm \
  && GO_TASK_VERSION=3.44.1 \
  && case ${TARGETPLATFORM} in \
    'linux/amd64') url="https://github.com/go-task/task/releases/download/v${GO_TASK_VERSION}/task_linux_amd64.tar.gz" ;; \
    'linux/arm64'*) url="https://github.com/go-task/task/releases/download/v${GO_TASK_VERSION}/task_linux_arm64.tar.gz" ;; \
    *) echo "Unsupported architecture: ${TARGETPLATFORM}" && exit 1 ;; \
  esac \
  && wget -O /tmp/task.tgz $url \
  && tar -xzf /tmp/task.tgz -C /usr/local/bin/

WORKDIR /app

COPY . ./

# Install npm dependencies and build assets
RUN cd web && npm install && npm run build

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
	\
	# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
	go mod download

# Build the server-only binary (excludes glaze/webview via -tags server).
# Same as the release linux binaries; only the linux-desktop variants include glaze.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -tags=server -ldflags="-s -w -X github.com/paularlott/llmrouter/build.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /usr/local/bin/llmrouter .

FROM ${DOCKER_HUB}library/alpine:3.22

ARG VERSION=0.0.1

# Upgrade to the latest versions
RUN apk update \
  && apk upgrade \
  && apk add bash

# Binary is built directly into /usr/local/bin in the builder stage
COPY --from=builder /usr/local/bin/llmrouter /usr/local/bin/llmrouter

# Add a user to run the process
RUN addgroup -S llmrouter \
  && adduser -S llmrouter -G llmrouter \
  && mkdir -p /data \
  && chown -R llmrouter:llmrouter /data

# Set user and working directory
USER llmrouter
WORKDIR /data

VOLUME [ "/data" ]

EXPOSE 12345/tcp

# Set the entrypoint
CMD ["/usr/local/bin/llmrouter", "server", "--host", "0.0.0.0"]

LABEL org.opencontainers.image.version=v${VERSION}
LABEL org.opencontainers.image.title=LLMRouter
LABEL org.opencontainers.image.description="LLM Routing Service - Routes requests to different LLM providers based on configuration"
LABEL org.opencontainers.image.url=https://github.com/paularlott/llmrouter
LABEL org.opencontainers.image.documentation=https://github.com/paularlott/llmrouter
LABEL org.opencontainers.image.vendor="Paul Arlott"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.source="https://github.com/paularlott/llmrouter"
