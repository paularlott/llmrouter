module github.com/paularlott/llmrouter

go 1.25.5

require (
	github.com/BurntSushi/toml v1.5.0
	github.com/paularlott/cli v0.6.0
	github.com/paularlott/logger v0.3.0
	github.com/paularlott/mcp v0.6.3
	github.com/paularlott/scriptling v0.5.0
	golang.org/x/net v0.48.0
)

require (
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/oauth2 v0.34.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)

// Use local versions for development
//replace github.com/paularlott/cli => /Users/paul/Code/Source/cli

//replace github.com/paularlott/mcp => /Users/paul/Code/Source/mcp
replace github.com/paularlott/scriptling => /Users/paul/Code/Source/scriptling
