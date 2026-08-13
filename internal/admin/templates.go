package admin

import (
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/paularlott/llmrouter/web"
)

// TemplateRenderer handles HTML template rendering
type TemplateRenderer struct {
	templates *template.Template
}

// NewTemplateRenderer creates a new template renderer
func NewTemplateRenderer(fsys fs.FS) *TemplateRenderer {
	// Parse all templates from the embedded filesystem
	tmpl := template.New("")

	// Walk the templates directory and parse all HTML files
	fs.WalkDir(fsys, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			// Read the template file
			content, err := fs.ReadFile(fsys, path)
			if err != nil {
				return err
			}

			// Get the template name (relative path from templates directory)
			name := strings.TrimPrefix(path, "templates/")
			tmpl = template.Must(tmpl.New(name).Parse(string(content)))
		}
		return nil
	})

	return &TemplateRenderer{templates: tmpl}
}

// TemplateData contains data passed to templates
type TemplateData struct {
	CSSFile string
	JSFile  string
	Prefix  string
}

// Render renders a template with the given data
func (r *TemplateRenderer) Render(w io.Writer, name string, data *TemplateData) error {
	return r.templates.ExecuteTemplate(w, name, data)
}

// ServeStatic serves static files from the embedded assets. Checks dist/assets/
// first (compiled CSS/JS), then dist/ root (static files from Vite publicDir).
func ServeStatic(w http.ResponseWriter, r *http.Request) {
	// Remove the /admin/assets/ prefix
	assetPath := strings.TrimPrefix(r.URL.Path, "/admin/assets/")

	// Try dist/assets/ first (CSS/JS), then dist/ root (favicon, etc. from publicDir)
	// Use path.Join (not filepath.Join) because embed.FS always uses forward
	// slashes regardless of OS — filepath.Join produces backslashes on Windows.
	var content []byte
	for _, prefix := range []string{"dist/assets", "dist"} {
		if c, err := web.Assets.ReadFile(path.Join(prefix, assetPath)); err == nil {
			content = c
			break
		}
	}
	if content == nil {
		http.NotFound(w, r)
		return
	}

	// Set content type based on extension
	ext := path.Ext(assetPath)
	switch ext {
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".ico":
		w.Header().Set("Content-Type", "image/x-icon")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Write(content)
}
