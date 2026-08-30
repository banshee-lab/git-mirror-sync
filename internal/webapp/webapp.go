package webapp

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func resolveDistRoot(root fs.FS) (fs.FS, error) {
	candidates := []string{"dist"}
	for _, candidate := range candidates {
		if _, err := fs.Stat(root, candidate+"/index.html"); err == nil {
			subFS, err := fs.Sub(root, candidate)
			if err != nil {
				return nil, fmt.Errorf("prepare embedded dist fs: %w", err)
			}
			return subFS, nil
		}
	}
	return nil, fmt.Errorf("embedded dist root not found")
}

// NewHandler returns a SPA handler serving the generated Angular build and falling back to index.html for client routes.
func NewHandler() (http.Handler, error) {
	rootFS, err := resolveDistRoot(embedded)
	if err != nil {
		fallback, readErr := fs.ReadFile(embedded, "index.html")
		if readErr != nil {
			return nil, fmt.Errorf("prepare embedded dist fs: %w", err)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fallback)
		}), nil
	}

	fileServer := http.FileServer(http.FS(rootFS))
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		if cleanPath == "" || strings.HasPrefix(cleanPath, "assets/") || strings.HasPrefix(cleanPath, "styles-") || strings.HasPrefix(cleanPath, "main-") || strings.HasSuffix(cleanPath, ".js") || strings.HasSuffix(cleanPath, ".css") || strings.HasSuffix(cleanPath, ".ico") || cleanPath == "favicon.ico" {
			fileServer.ServeHTTP(w, r)
			return
		}

		index, err := fs.ReadFile(rootFS, "index.html")
		if err != nil {
			http.Error(w, "index.html not available", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	}))
	return mux, nil
}
