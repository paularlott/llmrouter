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

  async init() {
    await this.loadData();
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
  editingServer: null,
  deletingServer: null,
  toolsServer: null,
  tools: [],
  loadingTools: false,
  toolsError: null,
  saving: false,
  deleting: false,
  formError: null,
  storageWritable: false,
  form: {
    namespace: "",
    url: "",
    auth_type: "bearer",
    token: "",
    oauth_token_url: "",
    oauth_access_token: "",
    oauth_refresh_token: "",
    enabled: true,
    tool_visibility: "native",
  },

  async init() {
    await Promise.all([this.loadServers(), this.checkStorageStatus()]);
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

  resetForm() {
    this.form = {
      namespace: "",
      url: "",
      auth_type: "bearer",
      token: "",
      oauth_token_url: "",
      oauth_access_token: "",
      oauth_refresh_token: "",
      enabled: true,
      tool_visibility: "native",
    };
    this.editingServer = null;
    this.formError = null;
  },

  editServer(server) {
    this.editingServer = server;
    this.form = {
      namespace: server.namespace,
      url: server.url,
      auth_type: server.auth_type || "bearer",
      token: "",
      oauth_token_url: "",
      oauth_access_token: "",
      oauth_refresh_token: "",
      enabled: server.enabled,
      tool_visibility: server.tool_visibility || "native",
    };
    this.showAddModal = true;
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

      const body = { ...this.form };
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
    const newEnabled = !tool.enabled;

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

      // Update local state on success
      tool.enabled = newEnabled;
    } catch (err) {
      // Revert on error - the UI will show the old state
      console.error("Failed to toggle tool:", err);
      this.toolsError = err.message;
      // Reload tools to get correct state
      this.loadTools();
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

  logout() {
    fetch("/admin/api/logout", { method: "POST" }).finally(() => {
      window.location.href = "/admin/login";
    });
  },
}));

// Make Alpine available globally
window.Alpine = Alpine;

// Start Alpine
Alpine.start();
