package router

import (
	"sort"
	"strings"
	"testing"

	mcplib "github.com/paularlott/mcp"
)

// Remote skills within one namespace are sorted into the prompt by URI, so
// a third-party server listing its skills in an arbitrary order cannot
// destabilize the system prompt (and defeat provider prompt caches).
func TestListChatSkillsSortsWithinRemoteNamespace(t *testing.T) {
	// Two namespaces, each with skills in non-alphabetical order, exercises
	// both the namespace sort and the within-namespace sort.
	results := []mcplib.RemoteSkillsResult{
		{Namespace: "zeta", Skills: []mcplib.Skill{
			{URI: "skill://zulu/SKILL.md", Frontmatter: map[string]any{"name": "zulu", "description": "Z"}},
			{URI: "skill://alpha/SKILL.md", Frontmatter: map[string]any{"name": "alpha", "description": "A"}},
		}},
		{Namespace: "alpha", Skills: []mcplib.Skill{
			{URI: "skill://mike/SKILL.md", Frontmatter: map[string]any{"name": "mike", "description": "M"}},
			{URI: "skill://bravo/SKILL.md", Frontmatter: map[string]any{"name": "bravo", "description": "B"}},
		}},
	}

	// Mirror listChatSkills: namespaces sorted first, then skills within
	// each namespace by URI.
	sort.Slice(results, func(i, j int) bool { return results[i].Namespace < results[j].Namespace })
	var lines []string
	for _, res := range results {
		skills := append([]mcplib.Skill{}, res.Skills...)
		sort.Slice(skills, func(i, j int) bool { return skills[i].URI < skills[j].URI })
		for _, skill := range skills {
			lines = append(lines, mcplib.SkillPromptLine(res.Namespace, skill))
		}
	}
	joined := strings.Join(lines, "\n")
	wantOrder := []string{"- alpha/bravo:", "- alpha/mike:", "- zeta/alpha:", "- zeta/zulu:"}
	last := -1
	for _, prefix := range wantOrder {
		idx := strings.Index(joined, prefix)
		if idx < 0 {
			t.Fatalf("missing %s in %s", prefix, joined)
		}
		if idx < last {
			t.Fatalf("lines not sorted: %s", joined)
		}
		last = idx
	}
}
