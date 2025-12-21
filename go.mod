module github.com/paularlott/llmrouter

go 1.25.5

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/dgraph-io/badger/v4 v4.9.0
	github.com/google/uuid v1.6.0
	github.com/paularlott/cli v0.6.0
	github.com/paularlott/logger v0.3.0
	github.com/paularlott/mcp v0.6.10
	github.com/paularlott/scriptling v0.0.0-20251213112516-e39fc4cc9d10
	golang.org/x/net v0.48.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgraph-io/ristretto/v2 v2.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/flatbuffers v25.2.10+incompatible // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.37.0 // indirect
	go.opentelemetry.io/otel/metric v1.37.0 // indirect
	go.opentelemetry.io/otel/trace v1.37.0 // indirect
	golang.org/x/oauth2 v0.34.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	google.golang.org/protobuf v1.36.7 // indirect
)

// Use local versions for development
//replace github.com/paularlott/cli => /Users/paul/Code/Source/cli

//replace github.com/paularlott/mcp => /Users/paul/Code/Source/mcp

//replace github.com/paularlott/scriptling => /Users/paul/Code/Source/scriptling
