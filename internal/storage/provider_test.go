package storage

import (
	"fmt"
	"testing"
)

// Providers stored with the removed model_allowlist keep their model list:
// it is folded into models, which win when both are set.
func TestParseProviderConfig_LegacyModelAllowlist(t *testing.T) {
	legacy, err := parseProviderConfig(map[string]any{
		"name": "p", "provider": "ollama",
		"model_allowlist": []any{"clef-flash:latest", "qwen3.8:27b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(legacy.Models) != "[clef-flash:latest qwen3.8:27b]" {
		t.Errorf("models = %v, want the legacy allowlist", legacy.Models)
	}

	both, err := parseProviderConfig(map[string]any{
		"name": "p", "provider": "ollama",
		"models":          []any{"a"},
		"model_allowlist": []any{"b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(both.Models) != "[a]" {
		t.Errorf("models = %v, want models to win", both.Models)
	}
}
