package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// handleStatic serves the built web UI from the embedded filesystem.
//
// Unknown paths fall back to index.html so client-side routes such as /tasks
// work on a hard refresh. Requests under /api are never handled here.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		writeError(w, http.StatusNotFound,
			"no web UI is bundled in this build; run `npm run build` in web/ and rebuild")
		return
	}

	// Resolve the request path against the filesystem, cleaning it so that
	// ".." cannot escape the embedded root.
	clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if clean == "" {
		clean = "index.html"
	}

	if target, err := s.static.Open(clean); err == nil {
		target.Close()
		// Vite fingerprints filenames under assets/, so they can be cached
		// indefinitely. index.html must never be, or updates would not be seen.
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.FileServerFS(s.static).ServeHTTP(w, r)
		return
	}

	index, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		writeError(w, http.StatusNotFound, "the bundled web UI is missing index.html")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(index)
}
