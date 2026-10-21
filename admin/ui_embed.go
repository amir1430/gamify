package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// embeddedUI is the console's built UI (admin/ui/dist, committed so go get needs no Node).
//
//go:embed all:ui/dist
var embeddedUI embed.FS

// ui is Config.UI, or the embedded build.
func (adm *Admin) ui() fs.FS {
	if adm.cfg.UI != nil {
		return adm.cfg.UI
	}
	sub, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		panic(err) // the path is a constant
	}
	return sub
}

// serveUI answers every request no route matched: unknown /api paths are a JSON 404; a GET of a
// built file serves it (hashed /assets/ cached for good); a missing asset is a 404; any other
// GET is a client-side route, so index.html.
func (adm *Admin) serveUI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "api" || strings.HasPrefix(p, "api/") || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		adm.fail(w, r, errNotFound)
		return
	}
	ui := adm.ui()
	if p != "" && p != "index.html" && fs.ValidPath(p) {
		if st, err := fs.Stat(ui, p); err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, ui, p)
			return
		}
	}
	if strings.HasPrefix(p, "assets/") {
		http.NotFound(w, r)
		return
	}
	// index.html names the current hashed assets: always revalidated. Served from its bytes, not
	// ServeFileFS, which redirects a request for /index.html.
	b, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		adm.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}
