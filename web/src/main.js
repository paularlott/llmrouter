import Alpine from "alpinejs";
import "./main.css";

// Dark mode store - persisted to localStorage
const _darkMode = localStorage.getItem("darkMode") === "true";
document.documentElement.classList.toggle("dark", _darkMode);

Alpine.store("darkMode", {
  value: _darkMode,

  toggle() {
    this.value = !this.value;
    localStorage.setItem("darkMode", this.value);
    document.documentElement.classList.toggle("dark", this.value);
  },
});

// Login form component
Alpine.data("loginForm", () => ({
  password: "",
  error: "",
  loading: false,

  async login() {
    this.error = "";
    this.loading = true;

    try {
      const formData = new URLSearchParams();
      formData.append("password", this.password);

      const response = await fetch("/admin/api/login", {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          "Accept": "application/json",
        },
        body: formData.toString(),
      });

      const data = await response.json();

      if (!response.ok) {
        throw new Error(data.error || "Login failed");
      }

      // Server sets HttpOnly cookie, just redirect
      // Check for return URL in query params
      const urlParams = new URLSearchParams(window.location.search);
      const returnUrl = urlParams.get("return") || "/admin/";
      window.location.href = returnUrl;
    } catch (err) {
      this.error = err.message;
    } finally {
      this.loading = false;
    }
  },
}));

// Dashboard component
Alpine.data("dashboard", () => ({
  stats: null,
  providers: [],
  allModels: [],
  loading: true,
  error: null,
  showModelsModal: false,
  selectedProvider: null,
  pollInterval: null,

  async init() {
    await this.loadData();
    // Start polling stats every 1 second
    this.pollInterval = setInterval(() => this.refreshStats(), 1000);
    // Stop polling when page is hidden, resume when visible
    document.addEventListener('visibilitychange', this.handleVisibility.bind(this));
  },

  destroy() {
    if (this.pollInterval) clearInterval(this.pollInterval);
    document.removeEventListener('visibilitychange', this.handleVisibility.bind(this));
  },

  handleVisibility() {
    if (document.hidden) {
      if (this.pollInterval) clearInterval(this.pollInterval);
    } else {
      this.refreshStats();
      this.pollInterval = setInterval(() => this.refreshStats(), 1000);
    }
  },

  async refreshStats() {
    try {
      const res = await fetch("/admin/api/stats");
      if (res.status === 401) {
        window.location.href = "/admin/login";
        return;
      }
      if (res.ok) {
        this.stats = await res.json();
      }
    } catch (err) {
      // Silently fail for polling
    }
  },

  async loadData() {
    this.loading = true;
    this.error = null;

    try {
      const [statsRes, providersRes, modelsRes] = await Promise.all([
        fetch("/admin/api/stats"),
        fetch("/admin/api/providers"),
        fetch("/admin/api/models"),
      ]);

      if (statsRes.status === 401 || providersRes.status === 401) {
        window.location.href = "/admin/login";
        return;
      }

      if (!statsRes.ok || !providersRes.ok || !modelsRes.ok) {
        throw new Error("Failed to load data");
      }

      this.stats = await statsRes.json();
      this.providers = await providersRes.json();
      this.allModels = await modelsRes.json();
    } catch (err) {
      this.error = err.message;
    } finally {
      this.loading = false;
    }
  },

  viewProviderModels(provider) {
    this.selectedProvider = provider;
    this.showModelsModal = true;
  },

  get providerModels() {
    if (!this.selectedProvider) return [];
    return this.allModels.filter(m => (m.providers || []).includes(this.selectedProvider.name));
  },

  logout() {
    fetch("/admin/api/logout", { method: "POST" }).finally(() => {
      window.location.href = "/admin/login";
    });
  },
}));

// MCP Servers component
Alpine.data("mcpServers", () => ({
  servers: [],
  loading: true,
  error: null,
  showAddModal: false,
  showDeleteModal: false,
  showToolsModal: false,
  showResourcesModal: false,
  showPromptsModal: false,
  editingServer: null,
  deletingServer: null,
  toolsServer: null,
  tools: [],
   loadingTools: false,
   toolsError: null,
   toolFilter: "",
   togglingTool: null,
  resourcesServer: null,
  resources: [],
  loadingResources: false,
  resourcesError: null,
  promptsServer: null,
  prompts: [],
  loadingPrompts: false,
  promptsError: null,
  // Tool call (live execution) state
  showToolCallModal: false,
  callingTool: null,
  toolCallArgs: {},
  toolCallHistory: [],
  callingToolInProgress: false,
  toolCallError: null,
  saving: false,
  deleting: false,
  refreshing: false,
  formError: null,
  storageWritable: false,
  form: {
    namespace: "",
    transport: "http",
    url: "",
    command: "",
    args: "",
    auth_type: "bearer",
    token: "",
    oauth_token_url: "",
    oauth_access_token: "",
    oauth_refresh_token: "",
    enabled: true,
    tool_visibility: "native",
    remote_search: false,
    notifications: false,
  },

  async init() {
    await Promise.all([this.loadServers(), this.checkStorageStatus()]);
  },

  get filteredTools() {
    const q = this.toolFilter.trim().toLowerCase();
    if (!q) return this.tools;
    return this.tools.filter((t) => t.name.toLowerCase().includes(q));
  },

  async checkStorageStatus() {
    try {
      const response = await fetch("/admin/api/mcp-storage-status");
      if (response.ok) {
        const data = await response.json();
        this.storageWritable = data.writable;
      }
    } catch (err) {
      // If we can't check status, assume not writable
      this.storageWritable = false;
    }
  },

  async loadServers() {
    this.loading = true;
    this.error = null;

    try {
      const response = await fetch("/admin/api/mcp-servers");

      if (response.status === 401) {
        window.location.href = "/admin/login";
        return;
      }

      if (!response.ok) throw new Error("Failed to load servers");
      this.servers = await response.json();
    } catch (err) {
      this.error = err.message;
    } finally {
      this.loading = false;
    }
  },

  async refreshCache() {
    this.refreshing = true;
    try {
      const response = await fetch("/admin/api/mcp-servers/refresh-cache", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      if (!response.ok) throw new Error("Failed to refresh cache");
      // Reload servers to show updated tool counts
      await this.loadServers();
    } catch (err) {
      this.error = err.message;
    } finally {
      this.refreshing = false;
    }
  },

  resetForm() {
    this.form = {
      namespace: "",
      transport: "http",
      url: "",
      command: "",
      args: "",
      auth_type: "bearer",
      token: "",
      oauth_token_url: "",
      oauth_access_token: "",
      oauth_refresh_token: "",
      enabled: true,
      tool_visibility: "native",
      remote_search: false,
      notifications: false,
    };
    this.editingServer = null;
    this.formError = null;
  },

  editServer(server) {
    this.editingServer = server;
    this.form = {
      namespace: server.namespace,
      transport: server.command ? "stdio" : "http",
      url: server.url,
      command: server.command || "",
      args: (server.args || []).join(" "),
      auth_type: server.auth_type || "bearer",
      token: "",
      oauth_token_url: "",
      oauth_access_token: "",
      oauth_refresh_token: "",
      enabled: server.enabled,
      tool_visibility: server.tool_visibility || "native",
      remote_search: server.remote_search || false,
      notifications: server.notifications || false,
    };
    this.showAddModal = true;
  },

  async reauth() {
    this.saving = true;
    this.formError = null;

    try {
      const response = await fetch('/admin/api/mcp-servers/oauth/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          namespace: this.form.namespace,
          url: this.form.url,
          tool_visibility: this.form.tool_visibility,
          enabled: this.form.enabled,
          remote_search: this.form.remote_search,
          callback_base: window.location.origin,
          reauth: true,
        }),
      });
      if (!response.ok) {
        const data = await response.json();
        throw new Error(data.error || 'Failed to start OAuth flow');
      }
      const { auth_url } = await response.json();
      window.location.href = auth_url;
    } catch (err) {
      this.formError = err.message;
      this.saving = false;
    }
  },

  async saveServer() {
    this.saving = true;
    this.formError = null;

    try {
      // OAuth2: initiate PKCE flow (new servers only)
      if (this.form.auth_type === 'oauth2' && !this.editingServer) {
        const response = await fetch('/admin/api/mcp-servers/oauth/start', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            namespace: this.form.namespace,
            url: this.form.url,
            tool_visibility: this.form.tool_visibility,
            enabled: this.form.enabled,
            remote_search: this.form.remote_search,
            callback_base: window.location.origin,
          }),
        });
        if (!response.ok) {
          const data = await response.json();
          throw new Error(data.error || 'Failed to start OAuth flow');
        }
        const { auth_url } = await response.json();
        window.location.href = auth_url;
        return;
      }

      const url = this.editingServer
        ? `/admin/api/mcp-servers/${this.editingServer.namespace}`
        : '/admin/api/mcp-servers';
      const method = this.editingServer ? 'PUT' : 'POST';

      // Build body from form, splitting args string into array and clearing
      // fields that don't apply to the chosen transport.
      const body = { ...this.form };
      if (body.transport === 'stdio') {
        body.url = '';
        body.auth_type = 'bearer';
        body.token = '';
        body.args = (body.args || '').trim().split(/\s+/).filter(Boolean);
      } else {
        body.command = '';
        body.args = [];
        delete body.args;
      }
      delete body.transport;
      delete body.oauth_access_token;
      delete body.oauth_refresh_token;
      if (this.editingServer && body.auth_type !== 'oauth2' && !body.token) {
        delete body.token;
      }

      const response = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });

      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to save server');
      }

      this.showAddModal = false;
      this.resetForm();
      await this.loadServers();
    } catch (err) {
      this.formError = err.message;
    } finally {
      this.saving = false;
    }
  },

  confirmDelete(server) {
    this.deletingServer = server;
    this.showDeleteModal = true;
  },

  async deleteServer() {
    this.deleting = true;

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${this.deletingServer.namespace}`,
        {
          method: "DELETE",
        }
      );

      if (!response.ok) throw new Error("Failed to delete server");

      this.showDeleteModal = false;
      this.deletingServer = null;
      await this.loadServers();
    } catch (err) {
      this.error = err.message;
    } finally {
      this.deleting = false;
    }
  },

  async toggleServerEnabled(server) {
    const newEnabled = !server.enabled;

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${server.namespace}/toggle`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            enabled: newEnabled,
          }),
        }
      );

      if (!response.ok) {
        const data = await response.json();
        throw new Error(data.error || "Failed to toggle server");
      }

      // Update local state on success
      server.enabled = newEnabled;
    } catch (err) {
      console.error("Failed to toggle server:", err);
      this.error = err.message;
      // Reload servers to get correct state
      this.loadServers();
    }
  },

  viewTools(server) {
    this.toolsServer = server;
    this.tools = [];
    this.toolsError = null;
    this.toolFilter = "";
    this.showToolsModal = true;
    this.loadTools();
  },

  async loadTools() {
    this.loadingTools = true;
    this.toolsError = null;

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${this.toolsServer.namespace}/tools`,
        {
        }
      );

      if (!response.ok) {
        const data = await response.json();
        throw new Error(data.error || "Failed to load tools");
      }

      const data = await response.json();
      this.tools = data.map((tool) => ({ ...tool, expanded: false }));
    } catch (err) {
      this.toolsError = err.message;
    } finally {
      this.loadingTools = false;
    }
  },

  async toggleTool(tool) {
    if (this.togglingTool === tool.name) return;
    const newEnabled = !tool.enabled;

    // Optimistic: flip immediately so the toggle feels responsive, then a
    // spinner shows while the change is confirmed server-side.
    tool.enabled = newEnabled;
    this.togglingTool = tool.name;

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${this.toolsServer.namespace}/tools/toggle`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            tool_name: tool.name,
            enabled: newEnabled,
          }),
        }
      );

      if (!response.ok) {
        const data = await response.json();
        throw new Error(data.error || "Failed to toggle tool");
      }
    } catch (err) {
      // Revert the optimistic flip on failure.
      console.error("Failed to toggle tool:", err);
      tool.enabled = !newEnabled;
      this.toolsError = err.message;
    } finally {
      this.togglingTool = null;
    }
  },

  openToolCall(tool) {
    this.callingTool = tool;
    this.toolCallError = null;
    this.toolCallHistory = [];
    this.toolCallArgs = {};
    // Seed defaults for each parameter so x-model binds cleanly.
    const props = (tool.input_schema && tool.input_schema.properties) || {};
    for (const [name, def] of Object.entries(props)) {
      if (def.type === "boolean") {
        this.toolCallArgs[name] = false;
      } else if (def.default !== undefined) {
        this.toolCallArgs[name] = def.default;
      } else {
        this.toolCallArgs[name] = "";
      }
    }
    this.showToolCallModal = true;
  },

  closeToolCall() {
    this.showToolCallModal = false;
  },

  resetToolCallArgs() {
    if (!this.callingTool) return;
    const props = (this.callingTool.input_schema && this.callingTool.input_schema.properties) || {};
    for (const [name, def] of Object.entries(props)) {
      if (def.type === "boolean") {
        this.toolCallArgs[name] = false;
      } else if (def.default !== undefined) {
        this.toolCallArgs[name] = def.default;
      } else {
        this.toolCallArgs[name] = "";
      }
    }
  },

  clearToolCallHistory() {
    this.toolCallHistory = [];
  },

  // Pretty-print tool text output when it's a JSON object/array; leave
  // plain text (and non-JSON) untouched. Only attempts a parse when the
  // trimmed string starts with { or [ so ordinary text is never mangled.
  formatToolOutput(text) {
    if (text == null) return "";
    if (typeof text !== "string") {
      try { return JSON.stringify(text, null, 2); } catch { return String(text); }
    }
    const s = text.trim();
    if (s === "") return "";
    const ch = s[0];
    if (ch === "{" || ch === "[") {
      try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return text; }
    }
    return text;
  },

  async runToolCall() {
    if (!this.callingTool || !this.toolsServer) return;
    this.toolCallError = null;

    const props = (this.callingTool.input_schema && this.callingTool.input_schema.properties) || {};
    const args = {};

    // Coerce form values to the types declared in the schema. Empty optional
    // fields are omitted; malformed JSON / numbers surface an inline error.
    try {
      for (const [name, def] of Object.entries(props)) {
        const raw = this.toolCallArgs[name];

        if (def.type === "boolean") {
          args[name] = !!raw;
          continue;
        }
        if (raw === undefined || raw === null || (typeof raw === "string" && raw.trim() === "")) {
          continue;
        }

        if (def.type === "integer") {
          const n = parseInt(raw, 10);
          if (isNaN(n)) throw new Error(`"${name}" is not a valid integer: ${raw}`);
          args[name] = n;
        } else if (def.type === "number") {
          const n = parseFloat(raw);
          if (isNaN(n)) throw new Error(`"${name}" is not a valid number: ${raw}`);
          args[name] = n;
        } else if (def.type === "array" || def.type === "object") {
          if (typeof raw === "string") {
            try {
              args[name] = JSON.parse(raw);
            } catch (e) {
              throw new Error(`"${name}" is not valid JSON: ${e.message}`);
            }
          } else {
            args[name] = raw;
          }
        } else {
          args[name] = raw;
        }
      }
    } catch (err) {
      this.toolCallError = err.message;
      return;
    }

    this.callingToolInProgress = true;
    const entry = { args: { ...args }, result: null, error: null, at: new Date().toLocaleTimeString() };

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${this.toolsServer.namespace}/tools/call`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ tool_name: this.callingTool.name, arguments: args }),
        }
      );

      const data = await response.json();
      if (!response.ok) {
        throw new Error(data.error || `Request failed (${response.status})`);
      }
      entry.result = data;
    } catch (err) {
      entry.error = err.message;
    }

    this.toolCallHistory.unshift(entry);
    this.callingToolInProgress = false;
  },

  viewResources(server) {
    this.resourcesServer = server;
    this.resources = [];
    this.resourcesError = null;
    this.showResourcesModal = true;
    this.loadResources();
  },

  async loadResources() {
    this.loadingResources = true;
    this.resourcesError = null;

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${this.resourcesServer.namespace}/resources`,
        {}
      );

      if (!response.ok) {
        const data = await response.json();
        throw new Error(data.error || "Failed to load resources");
      }

      const data = await response.json();
      this.resources = Array.isArray(data) ? data : [];
    } catch (err) {
      this.resourcesError = err.message;
    } finally {
      this.loadingResources = false;
    }
  },

  viewPrompts(server) {
    this.promptsServer = server;
    this.prompts = [];
    this.promptsError = null;
    this.showPromptsModal = true;
    this.loadPrompts();
  },

  async loadPrompts() {
    this.loadingPrompts = true;
    this.promptsError = null;

    try {
      const response = await fetch(
        `/admin/api/mcp-servers/${this.promptsServer.namespace}/prompts`,
        {}
      );

      if (!response.ok) {
        const data = await response.json();
        throw new Error(data.error || "Failed to load prompts");
      }

      const data = await response.json();
      this.prompts = Array.isArray(data) ? data : [];
    } catch (err) {
      this.promptsError = err.message;
    } finally {
      this.loadingPrompts = false;
    }
  },

  logout() {
    fetch("/admin/api/logout", { method: "POST" }).finally(() => {
      window.location.href = "/admin/login";
    });
  },
}));

// Models component
Alpine.data("models", () => ({
  models: [],
  loading: true,
  rescanning: false,
  error: null,
  search: "",

  async init() {
    try {
      const res = await fetch("/admin/api/models");
      if (res.status === 401) { window.location.href = "/admin/login"; return; }
      if (!res.ok) throw new Error("Failed to load models");
      this.models = (await res.json()).sort((a, b) => a.id.localeCompare(b.id));
    } catch (err) {
      this.error = err.message;
    } finally {
      this.loading = false;
    }
  },

  get filtered() {
    if (!this.search) return this.models;
    const q = this.search.toLowerCase();
    return this.models.filter(m => m.id.toLowerCase().includes(q));
  },

  async rescan() {
    this.rescanning = true;
    this.error = null;
    try {
      const res = await fetch("/admin/api/models/refresh", { method: "POST" });
      if (res.status === 401) { window.location.href = "/admin/login"; return; }
      if (!res.ok) throw new Error("Failed to rescan models");
      this.models = (await res.json()).sort((a, b) => a.id.localeCompare(b.id));
    } catch (err) {
      this.error = err.message;
    } finally {
      this.rescanning = false;
    }
  },

  logout() {
    fetch("/admin/api/logout", { method: "POST" }).finally(() => {
      window.location.href = "/admin/login";
    });
  },
}));

// x-trap: confine keyboard focus (Tab / Shift+Tab) to the element while the
// bound expression is truthy. Used on modal dialogs so keyboard and screen
// reader users can't Tab out into the page behind. On engage, focus moves
// into the dialog (first focusable descendant, else the container itself);
// on release it returns to the element that had focus before the dialog
// opened (typically the button that triggered it). Stacked modals — e.g. the
// tool-call modal opened over the tools modal — are supported via an internal
// stack: only the topmost trap intercepts Tab.
//
//   <div x-show="showModal" x-trap="showModal" role="dialog" aria-modal="true">
//
// NOTE: if @alpinejs/focus is ever added, it ships a same-named directive;
// drop this one in favour of the plugin.
const trapStack = [];

Alpine.directive(
  "trap",
  (el, { expression }, { effect, cleanup, evaluate }) => {
    const FOCUSABLE = [
      "a[href]",
      "button:not([disabled])",
      "textarea:not([disabled])",
      "input:not([disabled])",
      "select:not([disabled])",
      '[tabindex]:not([tabindex="-1"])',
    ].join(",");

    const focusables = () =>
      Array.from(el.querySelectorAll(FOCUSABLE)).filter((node) => {
        if (node.disabled || node.getAttribute("aria-hidden") === "true")
          return false;
        const { width, height } = node.getBoundingClientRect();
        return width > 0 && height > 0;
      });

    let lastFocused = null;
    let onKeyDown = null;

    const engage = () => {
      lastFocused = document.activeElement;
      trapStack.push(el);
      // Make the container itself focusable as a fallback target.
      el.setAttribute("tabindex", "-1");
      // Defer one frame so x-show has applied its display change before we
      // measure focusables and move focus.
      requestAnimationFrame(() => {
        // A deeper modal may have opened in the same frame; only focus in if
        // we're still on top.
        if (trapStack[trapStack.length - 1] !== el) return;
        const items = focusables();
        (items[0] || el).focus({ preventScroll: true });
      });
      onKeyDown = (e) => {
        // Yield to any trap opened above this one.
        if (trapStack[trapStack.length - 1] !== el) return;
        if (e.key !== "Tab") return;
        const items = focusables();
        if (items.length === 0) {
          e.preventDefault();
          el.focus({ preventScroll: true });
          return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        const active = document.activeElement;
        if (e.shiftKey && (active === first || !el.contains(active))) {
          e.preventDefault();
          last.focus({ preventScroll: true });
        } else if (!e.shiftKey && (active === last || !el.contains(active))) {
          e.preventDefault();
          first.focus({ preventScroll: true });
        }
      };
      el.addEventListener("keydown", onKeyDown, true);
    };

    const release = () => {
      if (onKeyDown) {
        el.removeEventListener("keydown", onKeyDown, true);
        onKeyDown = null;
      }
      const idx = trapStack.indexOf(el);
      if (idx !== -1) trapStack.splice(idx, 1);
      if (lastFocused && typeof lastFocused.focus === "function") {
        lastFocused.focus({ preventScroll: true });
      }
      lastFocused = null;
    };

    effect(() => {
      if (evaluate(expression)) engage();
      else release();
    });

    cleanup(release);
  },
);

// Make Alpine available globally
window.Alpine = Alpine;

// Start Alpine
Alpine.start();
