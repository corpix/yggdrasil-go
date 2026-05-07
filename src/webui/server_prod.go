//go:build !debug
// +build !debug

package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

//go:embed static/*
var staticFiles embed.FS

func setupStaticHandler(mux *http.ServeMux, server *WebUIServer) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic("failed to get embedded static files: " + err.Error())
	}

	staticHandler := http.FileServer(http.FS(staticFS))
	mux.HandleFunc("/static/", func(rw http.ResponseWriter, r *http.Request) {
		// CSS and JS are served without auth so the login page can load them
		path := strings.TrimPrefix(r.URL.Path, "/static/")
		if strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js") {
			// Strip the /static/ prefix before serving
			http.StripPrefix("/static/", staticHandler).ServeHTTP(rw, r)
			return
		}
		server.authMiddleware(func(rw http.ResponseWriter, r *http.Request) {
			// Strip the /static/ prefix before serving
			http.StripPrefix("/static/", staticHandler).ServeHTTP(rw, r)
		})(rw, r)
	})
}

func serveFile(rw http.ResponseWriter, r *http.Request, log core.Logger) {
	requestPath := strings.TrimPrefix(r.URL.Path, "/")
	if requestPath == "" {
		requestPath = "index.html"
	}

	filePath := "static/" + requestPath

	data, err := staticFiles.ReadFile(filePath)
	if err != nil {
		log.Debugf("File not found: %s", filePath)
		http.NotFound(rw, r)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(requestPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	rw.Header().Set("Content-Type", contentType)
	_, _ = rw.Write(data)

	log.Debugf("Served file: %s (type: %s)", filePath, contentType)
}
