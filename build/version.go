// Package build contains build-time version information
package build

// Version is the current version of llmrouter
// This should be updated for each release
const Version = "0.3.0"

// BuildDate can be set at compile time using ldflags
// e.g., go build -ldflags "-X github.com/paularlott/llmrouter/build.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var BuildDate string
