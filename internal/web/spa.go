package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves the embedded single-page app. Real files (index.html,
// hashed assets) are served directly. Any other path, such as the client-side
// route /tenant/tenant_042 that the browser requests on a deep link or refresh,
// falls back to index.html so Vue Router can take over. Unknown /api paths
// never reach here because the API routes are registered more specifically.
func spaHandler(root fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if isFile(root, name) {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r, root)
	})
}

// isFile reports whether name resolves to a regular file in the asset tree.
func isFile(root fs.FS, name string) bool {
	info, err := fs.Stat(root, name)
	return err == nil && !info.IsDir()
}

// serveIndex writes index.html with a 200 so the SPA boots and resolves the
// route client-side. If the bundle is missing index.html (a broken build),
// respond 404 rather than an empty 200.
func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	data, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
