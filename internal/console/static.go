package console

import (
	"embed"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The console SPA is embedded so a release binary is self-contained: the
// operator copies one binary and one factory.toml, not a web server tree.
//
//go:embed all:web/dist
var embeddedWeb embed.FS

func newStaticHandler(override fs.FS) (http.Handler, error) {
	web := override
	if web == nil {
		sub, err := fs.Sub(embeddedWeb, "web/dist")
		if err != nil {
			return nil, err
		}
		web = sub
	}
	if _, err := fs.Stat(web, "index.html"); err != nil {
		return nil, errors.New("console: embedded web assets are missing; run `make console-frontend` before building")
	}
	return &spaHandler{web: web}, nil
}

type spaHandler struct {
	web fs.FS
}

func (h *spaHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + strings.TrimPrefix(request.URL.Path, "/"))
	name := strings.TrimPrefix(clean, "/")
	if name == "" || name == "." {
		h.serveIndex(response, request)
		return
	}
	data, err := fs.ReadFile(h.web, name)
	if err != nil {
		// Single-page application: unknown paths are client routes, so the
		// shell is served and the hash router decides what to render.
		h.serveIndex(response, request)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	response.Header().Set("Content-Type", contentType)
	if strings.HasPrefix(name, "assets/") {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		response.Header().Set("Cache-Control", "no-store")
	}
	if request.Method == http.MethodHead {
		response.WriteHeader(http.StatusOK)
		return
	}
	_, _ = response.Write(data)
}

func (h *spaHandler) serveIndex(response http.ResponseWriter, request *http.Request) {
	data, err := fs.ReadFile(h.web, "index.html")
	if err != nil {
		http.Error(response, "console assets are unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodHead {
		response.WriteHeader(http.StatusOK)
		return
	}
	_, _ = response.Write(data)
}
