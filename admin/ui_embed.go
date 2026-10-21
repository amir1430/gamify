package admin

import (
	"embed"
	"io/fs"
	"net/http"
)

// embeddedUI is the console's built UI (admin/ui/dist, committed so go get needs no Node).
//
//go:embed ui/dist
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

// serveUI serves the UI's index.html at /.
func (adm *Admin) serveUI(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, adm.ui(), "index.html")
}
