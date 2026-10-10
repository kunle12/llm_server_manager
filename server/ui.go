package server

import (
	"io/fs"
	"net/http"
)

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
	a.router.PathPrefix("/ui/").Handler(http.StripPrefix("/ui/", http.FileServer(http.FS(dist))))
}
