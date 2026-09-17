// Package web is the built interface, compiled into the binary.
//
// There is no adjacent asset directory to deploy, and no second server to run:
// the files in dist/ are produced by `npm --prefix web run build` and read out
// of the executable itself.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist holds only public/.gitkeep until the frontend is built, so that
// `go build ./...` works on a machine with no Node toolchain — go:embed fails
// the build outright on a directory with nothing in it. Built() reports whether
// a real interface is in there.
//
//go:embed all:dist
var dist embed.FS

// Assets is dist/ with the directory prefix stripped, so /assets/app.js is
// exactly what it looks like.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Only reachable if the embed directive above stops matching, which is a
		// build-time fact rather than a runtime one.
		panic(err)
	}
	return sub
}

// Built reports whether the interface was compiled in. A binary built without
// running the frontend build still serves the whole API; it just has no pages.
func Built() bool {
	_, err := fs.Stat(Assets(), "index.html")
	return err == nil
}

// Handler serves the interface. Everything under /assets/ is content-addressed
// by Vite and may be cached forever; every other path is the single page, so a
// deep link like /browse/photos or /s/<token> is answered by the application
// rather than by a 404.
func Handler() http.Handler {
	assets := Assets()
	files := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Built() {
			http.Error(w, "this binary was built without the web interface: run `npm --prefix web ci && npm --prefix web run build` and rebuild. The API is unaffected.", http.StatusNotFound)
			return
		}

		h := w.Header()
		// The interface may load its own code and talk to its own API, and
		// nothing else. Stored files are served from /api/download under a far
		// stricter policy of their own; this one governs the application shell.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")

		name := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := fs.Stat(assets, name); name != "" && err == nil {
			if strings.HasPrefix(name, "assets/") {
				// Vite puts a content hash in these filenames, so a change is a
				// new URL and this copy is never the stale one.
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}

		// index.html names the current asset URLs, so a cached copy of it is a
		// page pointing at files that may no longer exist.
		h.Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, assets, "index.html")
	})
}
