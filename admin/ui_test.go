package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"testing"
)

func TestUIServed(t *testing.T) {
	_, adm, srv := newAdmin(t)
	get := func(p string) (*http.Response, []byte) {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}

	// The app and every client-side route: index.html.
	for _, p := range []string{"/", "/players/42"} {
		resp, b := get(p)
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") ||
			!bytes.Contains(b, []byte(`<div id="root">`)) {
			t.Errorf("GET %s: %d %s %.200q", p, resp.StatusCode, resp.Header.Get("Content-Type"), b)
		}
	}

	// An unknown API path is a JSON 404, never the app.
	resp, b := get("/api/nope")
	var e struct{ Code string }
	if resp.StatusCode != 404 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") ||
		json.Unmarshal(b, &e) != nil || e.Code != "not_found" {
		t.Errorf("GET /api/nope: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}

	// Built assets with their content types.
	entries, err := fs.ReadDir(adm.ui(), "assets")
	if err != nil {
		t.Fatalf("no built assets: %v", err)
	}
	want := map[string]string{".js": "text/javascript", ".css": "text/css"}
	seen := map[string]bool{}
	for _, en := range entries {
		ext := path.Ext(en.Name())
		ct, ok := want[ext]
		if !ok || seen[ext] {
			continue
		}
		seen[ext] = true
		resp, _ := get("/assets/" + en.Name())
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), ct) {
			t.Errorf("GET /assets/%s: %d %s", en.Name(), resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	if len(seen) != len(want) {
		t.Errorf("built assets: want a .js and a .css, have %v", seen)
	}

	// A missing asset is a 404, not the app.
	if resp, _ := get("/assets/nope.js"); resp.StatusCode != 404 {
		t.Errorf("GET /assets/nope.js: %d", resp.StatusCode)
	}
}
