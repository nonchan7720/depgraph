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

// webHandler serves the built UI from web at the site root.
func webHandler(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(web, "index.html"); err != nil {
			http.Error(w, "web 未ビルド: mise run web:build", http.StatusServiceUnavailable)
			return
		}
		files.ServeHTTP(w, r)
	})
}
