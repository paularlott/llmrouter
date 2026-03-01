ARG DOCKER_HUB

FROM --platform=${BUILDPLATFORM} ${DOCKER_HUB}library/golang:1.26.0-alpine AS builder

# Set build arguments
ARG TARGETPLATFORM

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

# Build the application for the target architecture
RUN echo "Building for target platform: ${TARGETPLATFORM}" \
  && case ${TARGETPLATFORM} in \
    'linux/amd64') task build-linux-amd64 ;; \
    'linux/arm64'*) task build-linux-arm64 ;; \
    *) echo "Unsupported target platform: ${TARGETPLATFORM}" && exit 1 ;; \
  esac

FROM ${DOCKER_HUB}library/alpine:3.22

ARG VERSION=0.0.1

# Upgrade to the latest versions
RUN apk update \
  && apk upgrade \
  && apk add bash

# Copy the main executable
COPY --from=builder /app/dist/llmrouter-linux-* /usr/local/bin/llmrouter

# Add a user to run the process
RUN addgroup -S llmrouter \
  && adduser -S llmrouter -G llmrouter \
  && mkdir -p /data \
  && chown -R llmrouter:llmrouter /data

# Set user and working directory
USER llmrouter
WORKDIR /data

VOLUME [ "/data" ]

EXPOSE 8080/tcp

# Set the entrypoint
CMD ["/usr/local/bin/llmrouter", "server"]

LABEL org.opencontainers.image.version=v${VERSION}
LABEL org.opencontainers.image.title=LLMRouter
LABEL org.opencontainers.image.description="LLM Routing Service - Routes requests to different LLM providers based on configuration"
LABEL org.opencontainers.image.url=https://github.com/paularlott/llmrouter
LABEL org.opencontainers.image.documentation=https://github.com/paularlott/llmrouter
LABEL org.opencontainers.image.vendor="Paul Arlott"
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.source="https://github.com/paularlott/llmrouter"
