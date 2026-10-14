package server

import (
	"io/fs"
	"net/http"
	"strings"
)

// uiCacheControl returns the Cache-Control header for an embedded UI asset
// (path relative to the dist root). Vite fingerprints asset filenames
// (index-<hash>.js) so those can be cached forever, but the entry document must
// always be revalidated: a stale cached index.html pins the browser to an old
// bundle after an upgrade, which is invisible until a hard refresh. Embedded
// files carry no ETag/Last-Modified, so "no-cache" forces a fresh read.
func uiCacheControl(p string) string {
	if strings.HasPrefix(p, "assets/") {
		return "public, max-age=31536000, immutable"
	}
	return "no-cache"
}

// registerUIRoutes serves the embedded web UI at /ui/ when it was compiled in
// (go build -tags embedui) and enabled at runtime with the -ui flag.
func (a *App) registerUIRoutes() {
	if !a.ServeUI {
		if uiEmbedded {
			a.logger("Embedded web UI compiled in; start with -ui to serve it at /ui/")
		}
		return
	}

	if !uiEmbedded {
		a.logger("Warning: -ui requested but the web UI was not embedded into this binary; build with: make build-ui (go build -tags embedui)")
		return
	}

	dist, err := fs.Sub(uiFS, "webui/dist")
	if err != nil {
		a.logger("Failed to open embedded UI assets: %v", err)
		return
	}

	a.logger("Serving web UI at /ui/")
	a.router.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	})
	fileServer := http.FileServer(http.FS(dist))
	a.router.PathPrefix("/ui/").Handler(http.StripPrefix("/ui/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", uiCacheControl(r.URL.Path))
		fileServer.ServeHTTP(w, r)
	})))
}
