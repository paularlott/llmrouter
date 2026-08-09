package router

import (
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
)

func TestResolveModelContext(t *testing.T) {
	cases := []struct {
		name       string
		provider   *Provider
		discovered int
		config     *types.Config
		want       int
	}{
		{
			name: "per-model override beats discovery",
			provider: &Provider{
				ModelContext:       map[string]int{"gpt-4o": 1000},
				DefaultContextSize: 9000,
			},
			discovered: 128000,
			want:       1000,
		},
		{
			name:       "discovered beats provider and global defaults",
			provider:   &Provider{DefaultContextSize: 9000},
			discovered: 128000,
			config:     &types.Config{Server: types.ServerConfig{DefaultContextSize: 7000}},
			want:       128000,
		},
		{
			name:     "provider default beats global",
			provider: &Provider{DefaultContextSize: 9000},
			config:   &types.Config{Server: types.ServerConfig{DefaultContextSize: 7000}},
			want:     9000,
		},
		{
			name:     "global default beats floor",
			provider: &Provider{},
			config:   &types.Config{Server: types.ServerConfig{DefaultContextSize: 7000}},
			want:     7000,
		},
		{
			name:     "4096 floor when nothing is set",
			provider: &Provider{},
			want:     defaultContextFloor,
		},
		{
			name:       "zero-valued per-model entry is ignored (falls through to discovery)",
			provider:   &Provider{ModelContext: map[string]int{"gpt-4o": 0}},
			discovered: 8192,
			want:       8192,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Router{config: tc.config}
			got := r.resolveModelContext(tc.provider, "gpt-4o", tc.discovered)
			if got != tc.want {
				t.Fatalf("resolveModelContext = %d, want %d", got, tc.want)
			}
		})
	}
}
