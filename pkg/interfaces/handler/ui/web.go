package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

// dist/ is the build output of web/ (pnpm build). Only dist/.gitkeep is
// committed, so a fresh checkout still compiles and / reports how to build.
//
//go:embed all:dist
var dist embed.FS

func embeddedWeb() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees dist/ exists
	}
	return sub
}

// NotBuiltMessage answers the UI (and the MCP App) when this binary was built without web/
// (the same two ways to install as the README).
const NotBuiltMessage = "This depgraph binary was built without the browser UI.\n" +
	"Use a release binary from https://github.com/nonchan7720/depgraph/releases,\n" +
	"or build from source: clone the repository and run `mise install && mise run build`."

// webHandler serves the built UI from web at the site root.
func webHandler(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(web, "index.html"); err != nil {
			http.Error(w, NotBuiltMessage, http.StatusServiceUnavailable)
			return
		}
		files.ServeHTTP(w, r)
	})
}
