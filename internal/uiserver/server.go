// Package uiserver serves the Hermes UI: a read-only, loopback-only web application that shows
// the documents Hermes already produces (inventory, assessment, migration plan). It cannot plan,
// apply, approve or cut over anything: every route is GET or HEAD, and the documents are loaded
// once at start-up.
package uiserver

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Documents holds the JSON documents to show. A nil entry means "not loaded"; the UI shows an
// empty state that names the command that produces it.
type Documents struct {
	Inventory  []byte
	Assessment []byte
	Plan       []byte
}

// Status is returned by /api/status so the UI knows what it can show.
type Status struct {
	Inventory  bool `json:"inventory"`
	Assessment bool `json:"assessment"`
	Plan       bool `json:"plan"`
	ReadOnly   bool `json:"read_only"`
}

const notBuiltPage = `<!doctype html><html lang="en"><meta charset="utf-8"><title>Hermes UI is not built</title>
<body style="font:16px/1.5 system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem">
<h1>Hermes UI is not built</h1>
<p>This binary was built without the web app. Run <code>make ui-build</code>, then <code>make build</code>.</p>
<p>The JSON API still works: <code>/api/status</code>.</p></body></html>`

// ValidateAddr accepts only loopback listen addresses. The UI has no authentication, so it must
// never be reachable from another machine.
func ValidateAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ui: invalid address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("ui: refusing to listen on %q: the UI has no authentication and only binds to loopback (127.0.0.1, ::1 or localhost)", addr)
}

// New returns the HTTP handler. Pass static=nil to use the embedded build.
func New(docs Documents, static fs.FS) (http.Handler, error) {
	if static == nil {
		sub, err := fs.Sub(distFS, "dist")
		if err != nil {
			return nil, err
		}
		static = sub
	}
	mux := http.NewServeMux()
	api := func(name string, body []byte) {
		mux.HandleFunc("/api/"+name, func(w http.ResponseWriter, r *http.Request) {
			if body == nil {
				http.Error(w, "document not loaded", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(body)
		})
	}
	api("inventory.json", docs.Inventory)
	api("assessment.json", docs.Assessment)
	api("plan.json", docs.Plan)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(Status{Inventory: docs.Inventory != nil, Assessment: docs.Assessment != nil, Plan: docs.Plan != nil, ReadOnly: true})
	})
	files := http.FileServer(http.FS(static))
	_, indexErr := fs.Stat(static, "index.html")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if indexErr != nil && !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if _, err := fs.Stat(static, p); err != nil {
				r.URL.Path = "/" // single-page app: unknown paths load the app shell
			}
		}
		files.ServeHTTP(w, r)
	})
	return guard(mux), nil
}

// guard enforces read-only access, a loopback Host header (DNS-rebinding defence) and
// restrictive browser headers. The app loads nothing from other origins.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "the Hermes UI is read-only", http.StatusMethodNotAllowed)
			return
		}
		if !loopbackHost(r.Host) {
			http.Error(w, "unexpected Host header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
