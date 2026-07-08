package router

import (
	"context"
	"strings"
	"time"
)

// augmentSystemPrompt is called by lmchatkit on every /api/chat request.
// It appends dynamic context to the persona's system prompt — currently
// a list of skill:// resources from the MCP server, so the model knows
// what skills it can retrieve via the get_skill tool (or equivalent).
//
// The augmentation is transient: the stored conversation keeps the
// original system prompt from the persona; this function's output is
// only sent to the LLM, never persisted.
//
// A short timeout is used so a slow/unreachable MCP server never blocks
// the chat — skills are a nice-to-have, not critical path.
func (r *Router) augmentSystemPrompt(ctx context.Context, current string) string {
	if r.mcpServer == nil || r.mcpServer.server == nil {
		return current
	}

	listCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	skills := r.listSkillResources(listCtx)
	if skills == "" {
		return current
	}

	var b strings.Builder
	b.WriteString(current)
	if !strings.HasSuffix(current, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n")
	b.WriteString(skills)
	return b.String()
}

// listSkillResources queries the MCP server for resources with a "skill://"
// URI scheme and returns a formatted string for the system prompt, or ""
// if no skill resources exist.
func (r *Router) listSkillResources(ctx context.Context) string {
	resources := r.mcpServer.server.ListResources(ctx)
	if len(resources) == 0 {
		return ""
	}

	var lines []string
	for _, res := range resources {
		if !strings.HasPrefix(res.URI, "skill://") {
			continue
		}
		if res.Description != "" {
			lines = append(lines, "- "+res.URI+": "+res.Description)
		} else {
			lines = append(lines, "- "+res.URI)
		}
	}

	if len(lines) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("The following skills are available. Call the lmchatkit__get_skill tool with the skill URI to retrieve detailed instructions:\n")
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
