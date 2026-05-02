package api

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johnbuluba/dockersnap/internal/dashboard"
)

// mountDashboard registers / and /ui/* on the router. Static assets are
// served from the embedded bundle; any unknown path under /ui/ falls back
// to index.html so client-side routing (wouter) survives a refresh on a
// deep link like /ui/instances/foo.
//
// The bare daemon URL (/) redirects to /ui/ so visitors land on the
// dashboard. The API surface stays under /api/v1.
func (s *Server) mountDashboard(r chi.Router) {
	sub, ok := dashboard.FS()
	if !ok {
		// No bundle present (someone ran `go build` without the ui:embed
		// step). Serve a useful message so users aren't stuck on a 404.
		r.Get("/", redirectTo("/ui/"))
		r.Get("/ui", redirectTo("/ui/"))
		r.Get("/ui/*", missingDashboardHandler)
		return
	}

	r.Get("/", redirectTo("/ui/"))
	r.Get("/ui", redirectTo("/ui/"))
	r.Handle("/ui/*", http.StripPrefix("/ui", spaHandler(sub)))
}

// spaHandler serves bundle files when they exist and falls back to
// index.html for unknown paths so wouter takes over on the client.
//
// We don't use http.FileServer because it canonicalizes paths ending in
// /index.html to ./ via 301 redirect — which would break the SPA fallback
// for any deep link like /ui/instances/foo. Reading the file directly and
// writing the bytes ourselves sidesteps that entirely.
func spaHandler(sub fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "/" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// Unknown path → SPA fallback.
			path = "index.html"
		}
		serveBundleFile(w, r, sub, path)
	})
}

// serveBundleFile reads the named file from sub and writes it as the
// response with the correct Content-Type. Uses http.ServeContent for
// Range support / If-Modified-Since handling, but with a fixed modtime
// (the daemon's start time would be more accurate but it's overkill —
// the embedded bundle is invariant for the life of the binary).
func serveBundleFile(w http.ResponseWriter, r *http.Request, sub fs.FS, path string) {
	f, err := sub.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "reading bundle file", http.StatusInternalServerError)
		return
	}

	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// index.html should never be cached — the asset URLs inside change
	// on every build. Hashed assets under /assets/ are immutable.
	if path == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
}

func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}
}

func missingDashboardHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(
		"dockersnap dashboard bundle not built.\n" +
			"\n" +
			"Run `task ui:build` to produce dashboard/dist, then rebuild the daemon.\n",
	))
}
