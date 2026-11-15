package main

import (
	"bytes"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nusapeta/route/web"
)

const staticCSP = "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; " +
	"img-src 'self' data: https://tile.openstreetmap.org; style-src 'self'; script-src 'self'; " +
	"connect-src 'self'; font-src 'self'; object-src 'none'"

var assetServer = http.StripPrefix("/assets/", http.FileServer(http.FS(web.Static())))

func (g *gateway) serveStatic(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/":
		g.serveIndex(w, r)
		return true
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		serveAsset(w, r)
		return true
	case r.URL.Path == "/assets":
		if allowRead(w, r) {
			http.NotFound(w, r)
		}
		return true
	}
	return false
}

func (g *gateway) serveIndex(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	setStaticHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body := bytes.ReplaceAll(web.Index(), []byte("{{ASSET_VERSION}}"), []byte(commit))
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(body))
}

func serveAsset(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	if path.Clean(r.URL.Path) != r.URL.Path || strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r)
		return
	}
	setStaticHeaders(w)
	assetServer.ServeHTTP(w, r)
}

func allowRead(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func setStaticHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", staticCSP)
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
