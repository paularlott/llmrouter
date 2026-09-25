package router

import (
	"context"
	"sort"
	"strings"

	mcplib "github.com/paularlott/mcp"
)

// augmentSystemPrompt is called by lmchatkit on every /api/chat request.
// It appends the available skills to the persona's system prompt: this
// router's own skills (skills-dir) plus the skills of every attached
// remote MCP server, listed as title + URI so the model knows what it can
// retrieve via the lmchatkit__get_skill tool (or resources/read).
//
// The augmentation is transient: the stored conversation keeps the
// original system prompt from the persona; this function's output is
// only sent to the LLM, never persisted.
func (r *Router) augmentSystemPrompt(ctx context.Context, current string) string {
	if r.mcpServer == nil || r.mcpServer.server == nil {
		return current
	}

	skills := r.listChatSkills(ctx)
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

// listChatSkills collects skills from the chat-side server's own registry
// and from every attached remote MCP server (remote lines carry the
// server's namespace so same-named skills stay distinguishable), formatted
// for the system prompt.
//
// Remote listings run through the shared mcplib.SkillsListingCache:
// fetched in parallel with a one-second budget each (skills are additive
// context, so one slow or dead remote must never starve the others — the
// first version shared one budget across the loop, and a single hanging
// initialize killed every server after it), cached for a minute so repeat
// chats don't pay the round trips again, and rendered in namespace order
// so the prompt stays deterministic.
func (r *Router) listChatSkills(ctx context.Context) string {
	var sources []mcplib.RemoteSkillsSource
	for namespace, rsClient := range r.mcpServer.remoteClients {
		if rsClient.client == nil || !rsClient.enabled {
			continue // nothing configured, or the operator disabled this server
		}
		sources = append(sources, mcplib.RemoteSkillsSource{Namespace: namespace, Client: rsClient.client})
	}

	results := r.skillCache.Listings(ctx, sources, func(namespace string, err error) {
		r.logger.Warn("skills listing: remote failed, skipping", "namespace", namespace, "error", err)
	})
	sort.Slice(results, func(i, j int) bool { return results[i].Namespace < results[j].Namespace })

	var lines []string
	for _, skill := range r.mcpServer.server.ListSkills() {
		lines = append(lines, mcplib.SkillPromptLine("", skill))
	}
	for _, res := range results {
		for _, skill := range res.Skills {
			lines = append(lines, mcplib.SkillPromptLine(res.Namespace, skill))
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
