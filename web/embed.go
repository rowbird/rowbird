// Package web embeds the built single-page application (web/dist) and serves it (ADR-0002).
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	return NewHandler(sub)
}

// notBuiltPage is shown by development builds made without `make build`. Release binaries always
// embed the UI, so end users never see it.
const notBuiltPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Rowbird</title></head>
<body><p>The web UI was not built into this binary. Run <code>make build</code>, or use <code>make dev</code>.</p></body></html>`

// NewHandler serves an SPA from fsys: existing files are served as they are, and any other path gets
// index.html so the client-side router can handle it. Hashed assets are cached forever; index.html
// is always revalidated so new releases are picked up.
func NewHandler(fsys fs.FS) http.Handler {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		index = nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")

		if name != "" && name != "index.html" {
			if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.ServeFileFS(w, r, fsys, name) //nolint:gosec // name is cleaned and fs.FS rejects paths outside its root
				return
			}
			// A missing build asset must not be answered with HTML, or the browser reports a
			// confusing MIME error instead of a 404.
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-cache")
		if index == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
