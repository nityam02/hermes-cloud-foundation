// Package uiserver serves the Hermes UI, a local control panel over the same Go packages the CLI uses.
//
// It is loopback only and authenticated: `hermes ui` prints a link with a random token, the token is
// exchanged for an HttpOnly SameSite=Strict cookie, every API call needs that cookie, every POST also
// needs a custom header (so a page on another site cannot trigger it) and a same-origin Origin, and
// requests whose Host header is not loopback are refused (DNS rebinding). The UI never accepts cloud
// credentials: it uses the operator's own sign-in on this machine.
package uiserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/connect"
)

//go:embed all:dist
var distFS embed.FS

// CookieName carries the session token.
const CookieName = "hermes_ui"

// Documents holds the JSON documents to show. A nil entry means "not loaded".
type Documents struct {
	Inventory  []byte
	Assessment []byte
	Plan       []byte
}

// DiscoverRequest starts the discovery, assessment and plan job.
type DiscoverRequest = connect.PipelineRequest

// Actions are the operations the UI can start. A nil function disables its endpoint, which is how the
// tests (and read-only embedding) run the server without any cloud access.
type Actions struct {
	AWSProfiles func() []string
	AWSIdentity func(ctx context.Context, profile, region string) (arn, account string, err error)
	CheckAWS    func(ctx context.Context, req connect.AWSRequest) connect.Result
	CheckGCP    func(ctx context.Context, req connect.GCPRequest) connect.Result
	// Discover runs discovery, assessment and planning, and returns the documents it produced.
	Discover func(ctx context.Context, req DiscoverRequest, report connect.Reporter) (Documents, error)
}

// Config configures the server.
type Config struct {
	Token   string // required: the session secret
	Docs    Documents
	Actions Actions
	Version string
}

// NewToken returns a random 256-bit token.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// ValidateAddr accepts only loopback listen addresses.
func ValidateAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ui: invalid address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	return fmt.Errorf("ui: refusing to listen on %q: the UI controls cloud operations and only binds to loopback (127.0.0.1, ::1 or localhost)", addr)
}

const notBuiltPage = `<!doctype html><html lang="en"><meta charset="utf-8"><title>Hermes UI is not built</title>
<body style="font:16px/1.5 system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem">
<h1>Hermes UI is not built</h1>
<p>This binary was built without the web app. Run <code>make ui-build</code>, then <code>make build</code>.</p></body></html>`

type server struct {
	cfg    Config
	mu     sync.RWMutex
	docs   Documents
	jobs   *jobs
	static fs.FS
	files  http.Handler
	index  bool
}

// New returns the HTTP handler. Pass static=nil to use the embedded build.
func New(cfg Config, static fs.FS) (http.Handler, error) {
	if len(cfg.Token) < 32 {
		return nil, fmt.Errorf("uiserver: a token of at least 32 characters is required")
	}
	if static == nil {
		sub, err := fs.Sub(distFS, "dist")
		if err != nil {
			return nil, err
		}
		static = sub
	}
	s := &server{cfg: cfg, docs: cfg.Docs, jobs: newJobs(), static: static, files: http.FileServer(http.FS(static))}
	_, err := fs.Stat(static, "index.html")
	s.index = err == nil

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.api(s.status))
	mux.HandleFunc("GET /api/inventory.json", s.api(s.doc(func(d Documents) []byte { return d.Inventory })))
	mux.HandleFunc("GET /api/assessment.json", s.api(s.doc(func(d Documents) []byte { return d.Assessment })))
	mux.HandleFunc("GET /api/plan.json", s.api(s.doc(func(d Documents) []byte { return d.Plan })))
	mux.HandleFunc("GET /api/connect/aws/profiles", s.api(s.awsProfiles))
	mux.HandleFunc("POST /api/connect/aws/identity", s.api(s.awsIdentity))
	mux.HandleFunc("POST /api/connect/aws/template", s.api(s.awsTemplate))
	mux.HandleFunc("POST /api/connect/aws/check", s.api(s.checkAWS))
	mux.HandleFunc("POST /api/connect/gcp/check", s.api(s.checkGCP))
	mux.HandleFunc("POST /api/jobs/discover", s.api(s.startDiscover))
	mux.HandleFunc("GET /api/jobs/{id}", s.api(s.getJob))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/", s.page)
	return s.guard(mux), nil
}

// guard applies the browser-facing protections to every request.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if !loopbackHost(r.Host) {
			http.Error(w, "unexpected Host header", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		case http.MethodPost:
			// A custom header forces a CORS preflight, which this server never grants, so a page on another
			// site cannot send it. The Origin check covers browsers that omit it from simple requests.
			if r.Header.Get("X-Hermes-UI") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "missing X-Hermes-UI header or JSON content type", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request refused", http.StatusForbidden)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		default:
			h.Set("Allow", "GET, HEAD, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) authed(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.cfg.Token)) == 1
}

// api wraps an endpoint that needs the session cookie.
func (s *server) api(fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeErr(w, http.StatusUnauthorized, "Open the link that `hermes ui` printed in your terminal. It carries the session token.")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		fn(w, r)
	}
}

// page serves the app. A valid ?token= is exchanged for the cookie and removed from the address.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	if t := r.URL.Query().Get("token"); t != "" {
		if subtle.ConstantTimeCompare([]byte(t), []byte(s.cfg.Token)) == 1 {
			// Not Secure: the UI is served over http on loopback, where a Secure cookie would not be sent by every
			// browser. HttpOnly and SameSite=Strict keep scripts and other sites away from it.
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.cfg.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}) //nolint:gosec // loopback http; see comment above
			// Always redirect to the root: never build a redirect from the request (open-redirect safe).
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if !s.index {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(notBuiltPage))
		return
	}
	if p := strings.TrimPrefix(path.Clean(r.URL.Path), "/"); p != "" {
		if _, err := fs.Stat(s.static, p); err != nil {
			r.URL.Path = "/" // single-page app: unknown paths load the app shell
		}
	}
	s.files.ServeHTTP(w, r)
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "request too large")
		return false
	}
	if err := json.Unmarshal(data, v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *server) snapshot() Documents {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docs
}

func (s *server) status(w http.ResponseWriter, _ *http.Request) {
	d := s.snapshot()
	a := s.cfg.Actions
	writeJSON(w, 200, map[string]any{
		"inventory": d.Inventory != nil, "assessment": d.Assessment != nil, "plan": d.Plan != nil,
		"version": s.cfg.Version,
		"can":     map[string]bool{"connect_aws": a.CheckAWS != nil, "connect_gcp": a.CheckGCP != nil, "discover": a.Discover != nil},
	})
}

func (s *server) doc(pick func(Documents) []byte) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		b := pick(s.snapshot())
		if b == nil {
			writeErr(w, 404, "document not loaded")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(b)
	}
}

func (s *server) awsProfiles(w http.ResponseWriter, _ *http.Request) {
	var p []string
	if s.cfg.Actions.AWSProfiles != nil {
		p = s.cfg.Actions.AWSProfiles()
	}
	writeJSON(w, 200, map[string]any{"profiles": p})
}

func (s *server) awsIdentity(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.AWSIdentity == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req struct{ Profile, Region string }
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	arn, account, err := s.cfg.Actions.AWSIdentity(ctx, req.Profile, req.Region)
	if err != nil {
		res := connect.CheckAWS(ctx, connect.AWSDeps{Identity: func(context.Context, string, string) (string, string, error) { return "", "", err }},
			connect.AWSRequest{Profile: req.Profile, Region: req.Region, RoleARN: "arn:aws:iam::000000000000:role/x", ExternalID: strings.Repeat("x", 16)})
		writeJSON(w, 200, map[string]any{"ok": false, "error": firstFix(res)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "arn": arn, "account": account, "root": connect.IsRootARN(arn)})
}

// firstFix returns the explanation of the first failed step, for identity errors.
func firstFix(r connect.Result) map[string]string {
	for _, st := range r.Steps {
		if st.Status == connect.Fail {
			return map[string]string{"detail": st.Detail, "fix": st.Fix}
		}
	}
	return map[string]string{"detail": "Could not read your AWS sign-in."}
}

// awsTemplate returns what the user needs to create the read-only role: a fresh external ID, the
// CloudFormation template, and the equivalent CLI command.
func (s *server) awsTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Principal, Region, ExternalID string }
	if !readJSON(w, r, &req) {
		return
	}
	if !strings.HasPrefix(req.Principal, "arn:aws") || connect.IsRootARN(req.Principal) {
		writeErr(w, 400, "principal must be the ARN of an IAM user or role (not the root user)")
		return
	}
	if req.Region == "" {
		req.Region = "us-east-1"
	}
	ext := req.ExternalID
	if len(ext) < 16 {
		ext = connect.NewExternalID()
	}
	writeJSON(w, 200, map[string]any{
		"external_id": ext,
		"role_name":   "HermesDiscoveryReadOnly",
		"template":    string(connect.RoleTemplate()),
		"command":     connect.CreateRoleCommand("hermes-discovery-role.json", ext, req.Principal, req.Region),
		"console_url": fmt.Sprintf("https://%s.console.aws.amazon.com/cloudformation/home?region=%s#/stacks/create", req.Region, req.Region),
	})
}

func (s *server) checkAWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.CheckAWS == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req connect.AWSRequest
	if !readJSON(w, r, &req) {
		return
	}
	writeJSON(w, 200, s.cfg.Actions.CheckAWS(r.Context(), req))
}

func (s *server) checkGCP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.CheckGCP == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req connect.GCPRequest
	if !readJSON(w, r, &req) {
		return
	}
	writeJSON(w, 200, s.cfg.Actions.CheckGCP(r.Context(), req))
}

func (s *server) startDiscover(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.Discover == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req DiscoverRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := connect.ValidateAWSRequest(req.AWS); err != nil {
		writeErr(w, 400, err.Error()+"; run the AWS connection check first")
		return
	}
	if req.Target != "" && req.Target != "gcp" && req.Target != "alibaba" {
		writeErr(w, 400, "target must be gcp or alibaba")
		return
	}
	id, ok := s.jobs.start("discover", 45*time.Minute, func(ctx context.Context, report func(step, status, detail string)) error {
		docs, err := s.cfg.Actions.Discover(ctx, req, report)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.docs = docs
		s.mu.Unlock()
		return nil
	})
	if !ok {
		writeErr(w, http.StatusConflict, "another job is still running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "no such job")
		return
	}
	writeJSON(w, 200, j)
}
