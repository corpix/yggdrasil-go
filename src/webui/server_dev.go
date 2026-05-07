//go:build debug
// +build debug

package webui

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

func setupStaticHandler(mux *http.ServeMux, server *WebUIServer) {
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.Dir("src/webui/static/")))
	mux.HandleFunc("/static/", func(rw http.ResponseWriter, r *http.Request) {
		// CSS and JS are served without auth so the login page can load them
		path := strings.TrimPrefix(r.URL.Path, "/static/")
		if strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js") {
			staticHandler.ServeHTTP(rw, r)
			return
		}
		server.authMiddleware(func(rw http.ResponseWriter, r *http.Request) {
			staticHandler.ServeHTTP(rw, r)
		})(rw, r)
	})
}

func serveFile(rw http.ResponseWriter, r *http.Request, log core.Logger) {
	requestPath := strings.TrimPrefix(r.URL.Path, "/")
	if requestPath == "" {
		requestPath = "index.html"
	}

	filePath := filepath.Join("src/webui/static", requestPath)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Debugf("File not found: %s", filePath)
		http.NotFound(rw, r)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(requestPath))
	if contentType != "" {
		rw.Header().Set("Content-Type", contentType)
	}

	log.Debugf("Serving file from disk: %s", filePath)
	http.ServeFile(rw, r, filePath)
}
