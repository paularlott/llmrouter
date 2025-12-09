package main

// Configuration types

type Config struct {
	Server   ServerConfig   `json:"server"`
	Logging  LoggingConfig  `json:"logging"`
	Providers []ProviderConfig `json:"providers"`
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

type ProviderConfig struct {
	Name      string   `json:"name"`
	BaseURL   string   `json:"base_url"`
	Token     string   `json:"token"`
	Enabled   bool     `json:"enabled"`
	Models    []string `json:"models,omitempty"`
	Allowlist []string `json:"allowlist,omitempty"`
	Denylist  []string `json:"denylist,omitempty"`
}