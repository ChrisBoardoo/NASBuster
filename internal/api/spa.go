// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const uiMissing = `<!doctype html><meta charset="utf-8"><title>CrossWax NasBuster</title>
<p>The web UI is not built into this binary. Run <code>pnpm build</code> in <code>web/</code>, then rebuild.</p>`

// spa serves the embedded UI. Unknown paths without a file extension fall
// back to index.html (client-side routing). Hashed assets are cached forever.
func (s *Server) spa() http.Handler {
	ui := s.d.UI
	hasIndex := false
	if ui != nil {
		if _, err := fs.Stat(ui, "index.html"); err == nil {
			hasIndex = true
		}
	}
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !hasIndex {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(uiMissing))
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			s.serveIndex(w, r)
			return
		}
		if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		s.serveIndex(w, r)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.d.UI, "index.html")
	if err != nil {
		http.Error(w, "index.html missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
