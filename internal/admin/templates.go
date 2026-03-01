package admin

import (
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
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
}

// Render renders a template with the given data
func (r *TemplateRenderer) Render(w io.Writer, name string, data *TemplateData) error {
	return r.templates.ExecuteTemplate(w, name, data)
}

// ServeStatic serves static files from the embedded assets
func ServeStatic(w http.ResponseWriter, r *http.Request) {
	// Remove the /admin/assets/ prefix
	path := strings.TrimPrefix(r.URL.Path, "/admin/assets/")
	path = filepath.Join("dist/assets", path)

	// Read the file from embedded FS
	content, err := web.Assets.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set content type based on extension
	ext := filepath.Ext(path)
	switch ext {
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.Write(content)
}
