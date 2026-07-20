package web

import (
	"embed"
	"io/fs"
)

// distFS holds the built single-page app. The repository ships a pre-built
// bundle so `go build` and the embedded binary work with no Node toolchain
// present; the Vite build (make web / the Docker node stage) overwrites this
// directory with an optimized bundle for production. The all: prefix keeps
// files whose names start with "_" or "." (Vite emits none today, but hashed
// asset layouts change).
//
//go:embed all:dist
var distFS embed.FS

// assets returns the SPA file tree rooted at dist, so paths line up with the
// URLs the browser requests ("/", "/assets/app.js", ...).
func assets() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
