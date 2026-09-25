package router

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

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

// skillCacheEntry is a cached per-remote skills listing.
type skillCacheEntry struct {
	skills []mcplib.Skill
	expiry time.Time
}

// listChatSkills collects skills from the chat-side server's own registry
// and from every attached remote MCP server (remote titles carry the
// server's namespace so same-named skills stay distinguishable), formatted
// for the system prompt.
//
// Remotes are fetched in parallel, each with its OWN one-second budget:
// skills are additive context, so one slow or dead remote must never
// starve the others (the first version shared one budget across the loop,
// and a single hanging initialize killed every server after it). Dead
// remotes are skipped with a visible warning; successful listings are
// cached for a minute so repeat chats don't pay the round trips again.
// Results render in namespace order — the prompt stays deterministic.
func (r *Router) listChatSkills(ctx context.Context) string {
	type remoteResult struct {
		namespace string
		skills    []mcplib.Skill
	}
	var (
		mu      sync.Mutex
		results []remoteResult
		wg      sync.WaitGroup
	)

	for namespace, rsClient := range r.mcpServer.remoteClients {
		if rsClient.client == nil || !rsClient.enabled {
			continue // nothing configured, or the operator disabled this server
		}
		if cached, ok := r.skillCache.Load(namespace); ok {
			if entry := cached.(skillCacheEntry); time.Now().Before(entry.expiry) {
				mu.Lock()
				results = append(results, remoteResult{namespace, entry.skills})
				mu.Unlock()
				continue
			}
		}
		wg.Add(1)
		go func(namespace string, rsClient *remoteServerClient) {
			defer wg.Done()
			remoteCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if err := rsClient.ensureInitialized(remoteCtx); err != nil {
				r.logger.Warn("skills listing: remote not reachable, skipping",
					"namespace", namespace, "error", err)
				return
			}
			skills, err := rsClient.client.ListSkills(remoteCtx)
			if err != nil {
				r.logger.Warn("skills listing: remote skills/list failed, skipping",
					"namespace", namespace, "error", err)
				return
			}
			r.skillCache.Store(namespace, skillCacheEntry{skills: skills, expiry: time.Now().Add(time.Minute)})
			mu.Lock()
			results = append(results, remoteResult{namespace, skills})
			mu.Unlock()
		}(namespace, rsClient)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].namespace < results[j].namespace })

	var lines []string
	for _, skill := range r.mcpServer.server.ListSkills() {
		lines = append(lines, skillLine("", skill))
	}
	for _, res := range results {
		for _, skill := range res.skills {
			lines = append(lines, skillLine(res.namespace, skill))
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

// skillLine renders one prompt line: "- name: description (uri)". The
// description is what lets the model decide a skill is relevant before
// spending a read on it; the name is the frontmatter name (names are
// labels), namespaced for remote servers.
func skillLine(namespace string, skill mcplib.Skill) string {
	name, _ := skill.Frontmatter["name"].(string)
	if name == "" {
		name = strings.TrimSuffix(strings.TrimPrefix(skill.URI, "skill://"), "/SKILL.md")
	}
	if namespace != "" {
		name = namespace + "/" + name
	}
	if description, _ := skill.Frontmatter["description"].(string); description != "" {
		return "- " + name + ": " + description + " (" + skill.URI + ")"
	}
	return "- " + name + " (" + skill.URI + ")"
}
