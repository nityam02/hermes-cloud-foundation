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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
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
	AWSProfiles func() []connect.AWSProfile
	AWSCLI      func(ctx context.Context) connect.AWSCLI
	AWSIdentity func(ctx context.Context, profile, region string) (arn, account string, err error)
	// AWSSignIn runs the AWS CLI's own browser sign-in.
	AWSSignIn func(ctx context.Context, req connect.AWSSignInRequest, p connect.Progress) error
	// AWSFindRole, AWSEnsureRole and AWSRemoveRole manage the read-only role stack with the operator's
	// own sign-in. They are the only AWS writes, and run only when the operator asks on the page.
	AWSFindRole   func(ctx context.Context, profile, region string) (*connect.RoleStack, error)
	AWSEnsureRole func(ctx context.Context, req connect.RoleRequest, p connect.Progress) (connect.RoleStack, error)
	AWSRemoveRole func(ctx context.Context, profile, region string, p connect.Progress) error
	CheckAWS      func(ctx context.Context, req connect.AWSRequest) connect.Result

	GCPStatus   func(ctx context.Context) connect.GCPState
	GCPInstall  func(ctx context.Context, p connect.Progress) error
	GCPSignIn   func(ctx context.Context, p connect.Progress) error
	GCPProjects func(ctx context.Context) ([]connect.GCPProject, error)
	// GCPEnableAPIs turns on the APIs Hermes uses, when the operator asks on the page.
	GCPEnableAPIs func(ctx context.Context, project string, p connect.Progress) error
	CheckGCP      func(ctx context.Context, req connect.GCPRequest) connect.Result

	// Discover runs discovery, assessment and planning, and returns the documents it produced.
	Discover func(ctx context.Context, req DiscoverRequest, report connect.Reporter) (Documents, error)
}

// Config configures the server.
type Config struct {
	Token   string // required: the session secret
	Docs    Documents
	Actions Actions
	Version string
	// StateDir keeps the remembered connection (connection.json). Empty keeps it in memory only.
	StateDir string
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
	store  *store
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
	s := &server{cfg: cfg, docs: cfg.Docs, jobs: newJobs(), store: newStore(cfg.StateDir), static: static, files: http.FileServer(http.FS(static))}
	_, err := fs.Stat(static, "index.html")
	s.index = err == nil

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.api(s.status))
	mux.HandleFunc("GET /api/inventory.json", s.api(s.doc(func(d Documents) []byte { return d.Inventory })))
	mux.HandleFunc("GET /api/assessment.json", s.api(s.doc(func(d Documents) []byte { return d.Assessment })))
	mux.HandleFunc("GET /api/plan.json", s.api(s.doc(func(d Documents) []byte { return d.Plan })))
	mux.HandleFunc("GET /api/connection", s.api(s.getConnection))
	mux.HandleFunc("POST /api/connection", s.api(s.setConnection))
	mux.HandleFunc("GET /api/connect/aws/profiles", s.api(s.awsProfiles))
	mux.HandleFunc("POST /api/connect/aws/scan", s.api(s.awsScan))
	mux.HandleFunc("POST /api/connect/aws/identity", s.api(s.awsIdentity))
	mux.HandleFunc("POST /api/connect/aws/signin", s.api(s.awsSignIn))
	mux.HandleFunc("POST /api/connect/aws/role", s.api(s.awsFindRole))
	mux.HandleFunc("POST /api/connect/aws/role/ensure", s.api(s.awsEnsureRole))
	mux.HandleFunc("POST /api/connect/aws/role/remove", s.api(s.awsRemoveRole))
	mux.HandleFunc("POST /api/connect/aws/template", s.api(s.awsTemplate))
	mux.HandleFunc("POST /api/connect/aws/setup-policy", s.api(s.awsSetupPolicy))
	mux.HandleFunc("POST /api/connect/aws/check", s.api(s.checkAWS))
	mux.HandleFunc("GET /api/connect/gcp/status", s.api(s.gcpStatus))
	mux.HandleFunc("POST /api/connect/gcp/install", s.api(s.gcpInstall))
	mux.HandleFunc("POST /api/connect/gcp/signin", s.api(s.gcpSignIn))
	mux.HandleFunc("GET /api/connect/gcp/projects", s.api(s.gcpProjects))
	mux.HandleFunc("POST /api/connect/gcp/apis", s.api(s.gcpEnableAPIs))
	mux.HandleFunc("POST /api/connect/gcp/check", s.api(s.checkGCP))
	mux.HandleFunc("POST /api/jobs/discover", s.api(s.startDiscover))
	mux.HandleFunc("GET /api/jobs/running", s.api(s.runningJobs))
	mux.HandleFunc("GET /api/jobs/{id}", s.api(s.getJob))
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.api(s.cancelJob))
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
		"can": map[string]bool{
			"connect_aws": a.CheckAWS != nil, "connect_gcp": a.CheckGCP != nil, "discover": a.Discover != nil,
			"aws_signin": a.AWSSignIn != nil, "aws_role": a.AWSEnsureRole != nil && a.AWSFindRole != nil,
			"gcp_signin": a.GCPSignIn != nil, "gcp_install": a.GCPInstall != nil, "gcp_apis": a.GCPEnableAPIs != nil,
		},
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

func (s *server) getConnection(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.store.get())
}

func (s *server) setConnection(w http.ResponseWriter, r *http.Request) {
	var c Connection
	if !readJSON(w, r, &c) {
		return
	}
	if err := s.store.set(c); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, c)
}

func (s *server) awsProfiles(w http.ResponseWriter, r *http.Request) {
	p := []connect.AWSProfile{}
	if s.cfg.Actions.AWSProfiles != nil {
		p = s.cfg.Actions.AWSProfiles()
	}
	var cli connect.AWSCLI
	if s.cfg.Actions.AWSCLI != nil {
		cli = s.cfg.Actions.AWSCLI(r.Context())
	}
	writeJSON(w, 200, map[string]any{"profiles": p, "cli": cli})
}

// identityResult is one profile's sign-in, as the page shows it.
type identityResult struct {
	Name    string            `json:"name,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	OK      bool              `json:"ok"`
	ARN     string            `json:"arn,omitempty"`
	Account string            `json:"account,omitempty"`
	Root    bool              `json:"root,omitempty"`
	Error   map[string]string `json:"error,omitempty"`
}

func (s *server) identity(ctx context.Context, profile, region string) identityResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	arn, account, err := s.cfg.Actions.AWSIdentity(ctx, profile, region)
	if err != nil {
		detail, fix := connect.ExplainAWSError(err, profile)
		return identityResult{Error: map[string]string{"detail": detail, "fix": fix}}
	}
	return identityResult{OK: true, ARN: arn, Account: account, Root: connect.IsRootARN(arn)}
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
	writeJSON(w, 200, s.identity(r.Context(), req.Profile, req.Region))
}

// awsScan checks every profile's sign-in at once (sts:GetCallerIdentity needs no permission), so the
// page can offer the ones that work instead of making the person guess.
func (s *server) awsScan(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.AWSIdentity == nil || s.cfg.Actions.AWSProfiles == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req struct{ Region string }
	if !readJSON(w, r, &req) {
		return
	}
	profiles := s.cfg.Actions.AWSProfiles()
	if len(profiles) > 20 {
		profiles = profiles[:20]
	}
	out := make([]identityResult, len(profiles))
	var wg sync.WaitGroup
	for i, p := range profiles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := s.identity(r.Context(), p.Name, req.Region)
			res.Name, res.Kind = p.Name, p.Kind
			out[i] = res
		}()
	}
	wg.Wait()
	writeJSON(w, 200, map[string]any{"profiles": out})
}

// startJob starts a job and answers with its id, or 409 when the lane is busy.
func (s *server) startJob(w http.ResponseWriter, lane, kind string, timeout time.Duration, fn jobFunc) {
	id, ok := s.jobs.start(lane, kind, timeout, fn)
	if !ok {
		running, _ := s.jobs.running(lane)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "another " + lane + " task is still running", "id": running})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *server) awsSignIn(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.AWSSignIn == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req connect.AWSSignInRequest
	if !readJSON(w, r, &req) {
		return
	}
	s.startJob(w, "aws", "aws-signin", 10*time.Minute, func(ctx context.Context, p progress) (any, error) {
		return nil, s.cfg.Actions.AWSSignIn(ctx, req, p)
	})
}

type profileRegion struct {
	Profile string `json:"profile"`
	Region  string `json:"region"`
}

func (s *server) awsFindRole(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.AWSFindRole == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req profileRegion
	if !readJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	st, err := s.cfg.Actions.AWSFindRole(ctx, req.Profile, req.Region)
	if err != nil {
		out := map[string]any{"found": false, "error": err.Error()}
		if errors.Is(err, connect.ErrSetupDenied) {
			out["denied"] = true
		}
		writeJSON(w, 200, out)
		return
	}
	writeJSON(w, 200, map[string]any{"found": st != nil && st.Status != "DELETE_COMPLETE", "stack": st})
}

func (s *server) awsEnsureRole(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.AWSEnsureRole == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req connect.RoleRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !strings.HasPrefix(req.Principal, "arn:aws") || connect.IsRootARN(req.Principal) {
		writeErr(w, 400, "principal must be the ARN of an IAM user or role (not the root user)")
		return
	}
	s.startJob(w, "aws", "aws-role", 15*time.Minute, func(ctx context.Context, p progress) (any, error) {
		return s.cfg.Actions.AWSEnsureRole(ctx, req, p)
	})
}

func (s *server) awsRemoveRole(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.AWSRemoveRole == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req profileRegion
	if !readJSON(w, r, &req) {
		return
	}
	s.startJob(w, "aws", "aws-role-remove", 15*time.Minute, func(ctx context.Context, p progress) (any, error) {
		return nil, s.cfg.Actions.AWSRemoveRole(ctx, req.Profile, req.Region, p)
	})
}

func (s *server) gcpStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.GCPStatus == nil {
		writeErr(w, 501, "not available")
		return
	}
	writeJSON(w, 200, s.cfg.Actions.GCPStatus(r.Context()))
}

func (s *server) gcpInstall(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Actions.GCPInstall == nil {
		writeErr(w, 501, "not available")
		return
	}
	s.startJob(w, "gcp", "gcp-install", 20*time.Minute, func(ctx context.Context, p progress) (any, error) {
		return nil, s.cfg.Actions.GCPInstall(ctx, p)
	})
}

func (s *server) gcpSignIn(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Actions.GCPSignIn == nil {
		writeErr(w, 501, "not available")
		return
	}
	s.startJob(w, "gcp", "gcp-signin", 10*time.Minute, func(ctx context.Context, p progress) (any, error) {
		return nil, s.cfg.Actions.GCPSignIn(ctx, p)
	})
}

func (s *server) gcpProjects(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.GCPProjects == nil {
		writeErr(w, 501, "not available")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	list, err := s.cfg.Actions.GCPProjects(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"projects": []connect.GCPProject{}, "error": err.Error()})
		return
	}
	if list == nil {
		list = []connect.GCPProject{}
	}
	writeJSON(w, 200, map[string]any{"projects": list})
}

func (s *server) gcpEnableAPIs(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Actions.GCPEnableAPIs == nil {
		writeErr(w, 501, "not available")
		return
	}
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	s.startJob(w, "gcp", "gcp-apis", 10*time.Minute, func(ctx context.Context, p progress) (any, error) {
		return nil, s.cfg.Actions.GCPEnableAPIs(ctx, req.ProjectID, p)
	})
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

// awsSetupPolicy returns the least-privilege policy that lets an operator manage the role stack.
func (s *server) awsSetupPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct{ Account string }
	if !readJSON(w, r, &req) {
		return
	}
	if req.Account != "" && !accountPattern.MatchString(req.Account) {
		writeErr(w, 400, "account must be 12 digits")
		return
	}
	writeJSON(w, 200, map[string]string{"policy": string(connect.SetupPolicy(req.Account))})
}

var accountPattern = regexp.MustCompile(`^[0-9]{12}$`)

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
	s.startJob(w, "discover", "discover", 45*time.Minute, func(ctx context.Context, p progress) (any, error) {
		docs, err := s.cfg.Actions.Discover(ctx, req, p.Report)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.docs = docs
		s.mu.Unlock()
		return nil, nil
	})
}

func (s *server) runningJobs(w http.ResponseWriter, _ *http.Request) {
	out := map[string]string{}
	for _, lane := range []string{"aws", "gcp", "discover"} {
		if id, ok := s.jobs.running(lane); ok {
			out[lane] = id
		}
	}
	writeJSON(w, 200, out)
}

func (s *server) cancelJob(w http.ResponseWriter, r *http.Request) {
	if !s.jobs.cancel(r.PathValue("id")) {
		writeErr(w, 404, "no such running job")
		return
	}
	writeJSON(w, 200, map[string]bool{"cancelled": true})
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "no such job")
		return
	}
	writeJSON(w, 200, j)
}
