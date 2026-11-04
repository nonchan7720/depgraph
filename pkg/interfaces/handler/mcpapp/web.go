package mcpapp

import (
	"embed"
	"io/fs"
)

// dist/ is the single-file build of web/ for MCP Apps (vite build --mode
// mcp-app: every script and style inlined, since the host's sandbox loads
// nothing). Only dist/.gitkeep is committed, so a fresh checkout still compiles.
//
//go:embed all:dist
var dist embed.FS

func embeddedApp() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees dist/ exists
	}
	return sub
}
