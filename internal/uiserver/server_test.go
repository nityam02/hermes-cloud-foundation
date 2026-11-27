package uiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/connect"
)

var testToken = strings.Repeat("a", 64)

func staticFS() fstest.MapFS {
	return fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/a.js": {Data: []byte("x")}}
}

func newHandler(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	cfg.Token = testToken
	h, err := New(cfg, staticFS())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type req struct {
	method, target, host, body string
	cookie                     bool
	headers                    map[string]string
}

func do(h http.Handler, q req) *httptest.ResponseRecorder {
	if q.host == "" {
		q.host = "127.0.0.1:8787"
	}
	r := httptest.NewRequest(q.method, q.target, strings.NewReader(q.body))
	r.Host = q.host
	if q.cookie {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: testToken})
	}
	for k, v := range q.headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(h http.Handler, target string, cookie bool) *httptest.ResponseRecorder {
	return do(h, req{method: "GET", target: target, cookie: cookie})
}

func post(h http.Handler, target, body string, extra map[string]string) *httptest.ResponseRecorder {
	hd := map[string]string{"X-Hermes-UI": "1", "Content-Type": "application/json"}
	for k, v := range extra {
		hd[k] = v
	}
	return do(h, req{method: "POST", target: target, body: body, cookie: true, headers: hd})
}

func TestTokenIsRequiredForEveryAPIRoute(t *testing.T) {
	h := newHandler(t, Config{Docs: Documents{Inventory: []byte(`{"kind":"Inventory"}`)}})
	for _, p := range []string{"/api/status", "/api/inventory.json", "/api/plan.json", "/api/connect/aws/profiles", "/api/jobs/abc", "/api/jobs/running", "/api/connection", "/api/connect/gcp/status", "/api/connect/gcp/projects"} {
		if w := get(h, p, false); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a cookie: %d", p, w.Code)
		}
	}
	if w := get(h, "/api/inventory.json", true); w.Code != 200 || !strings.Contains(w.Body.String(), "Inventory") {
		t.Fatalf("with the cookie: %d %s", w.Code, w.Body)
	}
	if w := get(h, "/api/plan.json", true); w.Code != 404 {
		t.Fatalf("missing document: %d", w.Code)
	}
}

func TestTokenInTheLinkBecomesAnHttpOnlyCookie(t *testing.T) {
	h := newHandler(t, Config{})
	w := get(h, "/?token="+testToken, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("expected a redirect that drops the token: %d %q", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != CookieName || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode || c[0].Value != testToken {
		t.Fatalf("cookie: %+v", c)
	}
	// A redirect must never point at another host, whatever path the request uses (open redirect).
	for _, target := range []string{"//evil.example/x?token=" + testToken, "/\\evil.example?token=" + testToken, "///evil.example?token=" + testToken} {
		w := get(h, target, false)
		loc := w.Header().Get("Location")
		u, err := url.Parse(loc)
		if loc != "" && (err != nil || u.Host != "" || strings.HasPrefix(loc, "//")) {
			t.Errorf("%s: redirect to %q leaves this server", target, loc)
		}
	}
	if w := get(h, "/?token=wrong", false); w.Code != http.StatusForbidden {
		t.Fatalf("a wrong token must be refused: %d", w.Code)
	}
	if _, err := New(Config{Token: "short"}, staticFS()); err == nil {
		t.Fatal("a short token must be refused")
	}
}

func TestPostsNeedTheHeaderJSONAndSameOrigin(t *testing.T) {
	called := 0
	h := newHandler(t, Config{Actions: Actions{CheckGCP: func(context.Context, connect.GCPRequest) connect.Result { called++; return connect.Result{OK: true} }}})
	body := `{"project_id":"demo-sandbox-1","region":"europe-north1"}`
	if w := post(h, "/api/connect/gcp/check", body, nil); w.Code != 200 || called != 1 {
		t.Fatalf("legitimate post: %d", w.Code)
	}
	// A page on another site cannot set the custom header; and a form post cannot send JSON.
	if w := do(h, req{method: "POST", target: "/api/connect/gcp/check", body: body, cookie: true, headers: map[string]string{"Content-Type": "application/json"}}); w.Code != http.StatusForbidden {
		t.Errorf("missing X-Hermes-UI: %d", w.Code)
	}
	if w := do(h, req{method: "POST", target: "/api/connect/gcp/check", body: body, cookie: true, headers: map[string]string{"X-Hermes-UI": "1", "Content-Type": "text/plain"}}); w.Code != http.StatusForbidden {
		t.Errorf("wrong content type: %d", w.Code)
	}
	if w := post(h, "/api/connect/gcp/check", body, map[string]string{"Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Errorf("cross-origin: %d", w.Code)
	}
	if w := post(h, "/api/connect/gcp/check", body, map[string]string{"Origin": "http://127.0.0.1:8787"}); w.Code != 200 {
		t.Errorf("same-origin must work: %d", w.Code)
	}
	if w := do(h, req{method: "POST", target: "/api/connect/gcp/check", body: body, headers: map[string]string{"X-Hermes-UI": "1", "Content-Type": "application/json"}}); w.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d", w.Code)
	}
	if called != 2 {
		t.Errorf("only the two legitimate calls may reach the action, got %d", called)
	}
	for _, m := range []string{"PUT", "DELETE", "PATCH"} {
		if w := do(h, req{method: m, target: "/api/status", cookie: true}); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
}

func TestHostGuardAndSecurityHeaders(t *testing.T) {
	h := newHandler(t, Config{})
	for _, host := range []string{"evil.example.com", "evil.example.com:8787", "10.0.0.5:8787"} {
		if w := do(h, req{method: "GET", target: "/", host: host}); w.Code != http.StatusForbidden {
			t.Errorf("Host %q: %d", host, w.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:1", "localhost", "[::1]:9"} {
		if w := do(h, req{method: "GET", target: "/", host: host}); w.Code != 200 {
			t.Errorf("Host %q: %d", host, w.Code)
		}
	}
	w := get(h, "/inventory/deep/link", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "app") {
		t.Fatalf("SPA fallback: %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'self'") || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers: %v", w.Header())
	}
}

func TestDisabledActionsAnswer501(t *testing.T) {
	h := newHandler(t, Config{})
	if w := post(h, "/api/connect/aws/check", `{}`, nil); w.Code != 501 {
		t.Fatalf("%d", w.Code)
	}
	var st struct{ Can map[string]bool }
	_ = json.Unmarshal(get(h, "/api/status", true).Body.Bytes(), &st)
	if st.Can["discover"] || st.Can["connect_aws"] {
		t.Fatalf("status must say what is available: %+v", st)
	}
}

func TestAWSTemplateRefusesRootAndGeneratesTheExternalID(t *testing.T) {
	h := newHandler(t, Config{})
	if w := post(h, "/api/connect/aws/template", `{"principal":"arn:aws:iam::123456789012:root"}`, nil); w.Code != 400 {
		t.Fatalf("root must be refused: %d", w.Code)
	}
	w := post(h, "/api/connect/aws/template", `{"principal":"arn:aws:iam::123456789012:user/op","region":"eu-north-1"}`, nil)
	var out struct{ External_id, Template, Command, Console_url string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.External_id) < 16 || !strings.Contains(out.Template, "AWS::IAM::Role") || !strings.Contains(out.Command, "CAPABILITY_NAMED_IAM") || !strings.Contains(out.Console_url, "eu-north-1") {
		t.Fatalf("%d %+v", w.Code, out)
	}
	if strings.Contains(out.Template, "NoEcho") {
		t.Error("the external ID must be prefillable")
	}
}

func waitJob(t *testing.T, h http.Handler, id string) Job {
	t.Helper()
	for i := 0; i < 200; i++ {
		var j Job
		_ = json.Unmarshal(get(h, "/api/jobs/"+id, true).Body.Bytes(), &j)
		if j.State != "running" {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Job{}
}

func TestDiscoverJobRunsOnceReportsStepsAndUpdatesDocuments(t *testing.T) {
	release := make(chan struct{})
	h := newHandler(t, Config{Actions: Actions{Discover: func(ctx context.Context, _ DiscoverRequest, report connect.Reporter) (Documents, error) {
		report("discover", "running", "reading")
		<-release
		report("discover", "done", "found 3")
		return Documents{Inventory: []byte(`{"kind":"Inventory"}`), Assessment: []byte(`{"kind":"Assessment"}`)}, nil
	}}})
	good := `{"aws":{"role_arn":"arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly","external_id":"hermes-0123456789abcdef","region":"eu-north-1"},"target":"gcp"}`

	if w := post(h, "/api/jobs/discover", `{"aws":{"role_arn":"nope","external_id":"x"}}`, nil); w.Code != 400 {
		t.Fatalf("invalid input must be refused before any job starts: %d", w.Code)
	}
	w := post(h, "/api/jobs/discover", good, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var id struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &id)
	if w2 := post(h, "/api/jobs/discover", good, nil); w2.Code != http.StatusConflict {
		t.Fatalf("a second job while one runs: %d", w2.Code)
	}
	if get(h, "/api/inventory.json", true).Code != 404 {
		t.Fatal("documents must not appear before the job finishes")
	}
	close(release)
	j := waitJob(t, h, id.ID)
	if j.State != "done" || len(j.Steps) != 1 || j.Steps[0].Status != "done" || j.Steps[0].Detail != "found 3" {
		t.Fatalf("%+v", j)
	}
	if get(h, "/api/inventory.json", true).Code != 200 || get(h, "/api/assessment.json", true).Code != 200 {
		t.Fatal("documents must be served after the job")
	}
	if w := post(h, "/api/jobs/discover", good, nil); w.Code != http.StatusAccepted {
		t.Fatalf("a new job may start after the first finished: %d", w.Code)
	}
}

func TestFailedJobKeepsTheOldDocuments(t *testing.T) {
	h := newHandler(t, Config{Docs: Documents{Plan: []byte(`{"kind":"MigrationPlan"}`)}, Actions: Actions{Discover: func(context.Context, DiscoverRequest, connect.Reporter) (Documents, error) {
		return Documents{}, errors.New("AccessDenied")
	}}})
	good := `{"aws":{"role_arn":"arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly","external_id":"hermes-0123456789abcdef","region":"eu-north-1"}}`
	var id struct{ ID string }
	_ = json.Unmarshal(post(h, "/api/jobs/discover", good, nil).Body.Bytes(), &id)
	j := waitJob(t, h, id.ID)
	if j.State != "failed" || !strings.Contains(j.Error, "AccessDenied") {
		t.Fatalf("%+v", j)
	}
	if get(h, "/api/plan.json", true).Code != 200 {
		t.Fatal("a failed run must not wipe the documents already loaded")
	}
}

func TestNotBuiltPage(t *testing.T) {
	cfg := Config{Token: testToken}
	h, err := New(cfg, fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	if w := get(h, "/", false); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "make ui-build") {
		t.Fatalf("%d", w.Code)
	}
}

func TestValidateAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:8787", "localhost:8787", "[::1]:8787"} {
		if err := ValidateAddr(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:8787", ":8787", "192.168.1.5:8787", "example.com:80", "nonsense"} {
		if err := ValidateAddr(bad); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestSignInJobPassesOnTheLinkAndCanBeCancelled(t *testing.T) {
	h := newHandler(t, Config{Actions: Actions{AWSSignIn: func(ctx context.Context, req connect.AWSSignInRequest, p connect.Progress) error {
		p.Step("signin", "running", "waiting for the browser")
		p.Prompt("https://signin.aws.amazon.com/v1/authorize?x", "")
		p.Line("Attempting to open your default browser.")
		<-ctx.Done()
		return ctx.Err()
	}}})
	w := post(h, "/api/connect/aws/signin", `{"profile":"hermes","region":"us-east-1"}`, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var id struct{ ID string }
	_ = json.Unmarshal(w.Body.Bytes(), &id)
	// A second AWS task waits; the page can find the running one again after a reload.
	if w := post(h, "/api/connect/aws/signin", `{"profile":"hermes"}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), id.ID) {
		t.Fatalf("second sign-in: %d %s", w.Code, w.Body)
	}
	var running map[string]string
	_ = json.Unmarshal(get(h, "/api/jobs/running", true).Body.Bytes(), &running)
	if running["aws"] != id.ID {
		t.Fatalf("running: %v", running)
	}
	var j Job
	for i := 0; i < 100 && j.Link == ""; i++ {
		_ = json.Unmarshal(get(h, "/api/jobs/"+id.ID, true).Body.Bytes(), &j)
		time.Sleep(5 * time.Millisecond)
	}
	if j.Link == "" || len(j.Log) != 1 {
		t.Fatalf("the link and output must reach the page: %+v", j)
	}
	if w := post(h, "/api/jobs/"+id.ID+"/cancel", `{}`, nil); w.Code != 200 {
		t.Fatalf("cancel: %d", w.Code)
	}
	j = waitJob(t, h, id.ID)
	if j.State != "cancelled" || j.Steps[0].Status != "failed" {
		t.Fatalf("%+v", j)
	}
	if w := post(h, "/api/connect/aws/signin", `{"profile":"hermes"}`, nil); w.Code != http.StatusAccepted {
		t.Fatalf("a new sign-in may start after the cancel: %d", w.Code)
	}
}

func TestLanesLetAWSAndGoogleWorkAtTheSameTime(t *testing.T) {
	release := make(chan struct{})
	block := func(ctx context.Context, _ connect.Progress) error { <-release; return nil }
	h := newHandler(t, Config{Actions: Actions{
		AWSSignIn: func(ctx context.Context, _ connect.AWSSignInRequest, p connect.Progress) error { return block(ctx, p) },
		GCPSignIn: block,
	}})
	defer close(release)
	if w := post(h, "/api/connect/aws/signin", `{"profile":"a"}`, nil); w.Code != http.StatusAccepted {
		t.Fatalf("aws: %d", w.Code)
	}
	if w := post(h, "/api/connect/gcp/signin", `{}`, nil); w.Code != http.StatusAccepted {
		t.Fatalf("google must not wait for AWS: %d", w.Code)
	}
}

func TestRoleEnsureRefusesRootAndReturnsTheRole(t *testing.T) {
	h := newHandler(t, Config{Actions: Actions{
		AWSFindRole: func(context.Context, string, string) (*connect.RoleStack, error) { return nil, nil },
		AWSEnsureRole: func(_ context.Context, req connect.RoleRequest, p connect.Progress) (connect.RoleStack, error) {
			p.Step("create", "done", "Created.")
			return connect.RoleStack{Status: "CREATE_COMPLETE", RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", ExternalID: "hermes-x-0000000000", TrustedPrincipal: req.Principal}, nil
		},
	}})
	if w := post(h, "/api/connect/aws/role/ensure", `{"principal":"arn:aws:iam::123456789012:root"}`, nil); w.Code != 400 {
		t.Fatalf("root: %d", w.Code)
	}
	var found struct{ Found bool }
	_ = json.Unmarshal(post(h, "/api/connect/aws/role", `{"profile":"p"}`, nil).Body.Bytes(), &found)
	if found.Found {
		t.Fatal("no stack yet")
	}
	var id struct{ ID string }
	_ = json.Unmarshal(post(h, "/api/connect/aws/role/ensure", `{"profile":"p","principal":"arn:aws:iam::123456789012:user/op"}`, nil).Body.Bytes(), &id)
	j := waitJob(t, h, id.ID)
	var st connect.RoleStack
	_ = json.Unmarshal(j.Result, &st)
	if j.State != "done" || st.RoleARN == "" || st.TrustedPrincipal != "arn:aws:iam::123456789012:user/op" {
		t.Fatalf("%+v %+v", j, st)
	}
}

func TestScanChecksEveryProfile(t *testing.T) {
	h := newHandler(t, Config{Actions: Actions{
		AWSProfiles: func() []connect.AWSProfile {
			return []connect.AWSProfile{{Name: "default", Kind: "other"}, {Name: "op", Kind: "login"}, {Name: "rooty", Kind: "login"}}
		},
		AWSIdentity: func(_ context.Context, profile, _ string) (string, string, error) {
			switch profile {
			case "op":
				return "arn:aws:iam::123456789012:user/op", "123456789012", nil
			case "rooty":
				return "arn:aws:iam::123456789012:root", "123456789012", nil
			}
			return "", "", errors.New("failed to refresh cached credentials, no EC2 IMDS role found")
		},
	}})
	var out struct{ Profiles []identityResult }
	_ = json.Unmarshal(post(h, "/api/connect/aws/scan", `{"region":"us-east-1"}`, nil).Body.Bytes(), &out)
	if len(out.Profiles) != 3 {
		t.Fatalf("%+v", out)
	}
	byName := map[string]identityResult{}
	for _, p := range out.Profiles {
		byName[p.Name] = p
	}
	if byName["default"].OK || !strings.Contains(byName["default"].Error["fix"], "Sign in to AWS") {
		t.Errorf("default: %+v", byName["default"])
	}
	if !byName["op"].OK || byName["op"].Root || byName["op"].Kind != "login" {
		t.Errorf("op: %+v", byName["op"])
	}
	if !byName["rooty"].Root {
		t.Errorf("root must be flagged: %+v", byName["rooty"])
	}
}

func TestConnectionIsRememberedOnDiskWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	h := newHandler(t, Config{StateDir: dir})
	body := `{"aws":{"profile":"op","region":"us-east-1","role_arn":"arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly","external_id":"hermes-0123456789abcdef","ok":true,"secret_access_key":"nope"},"gcp":{"project_id":"alpha-sandbox","region":"europe-north1"},"regions":"us-east-1","token":"nope"}`
	if w := post(h, "/api/connection", body, nil); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	data, err := os.ReadFile(filepath.Join(dir, "connection.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "nope") || !strings.Contains(string(data), "alpha-sandbox") {
		t.Fatalf("only known, non-secret fields are kept: %s", data)
	}
	if st, _ := os.Stat(filepath.Join(dir, "connection.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	// A new server (a restart) starts from what was saved.
	h2 := newHandler(t, Config{StateDir: dir})
	var c Connection
	_ = json.Unmarshal(get(h2, "/api/connection", true).Body.Bytes(), &c)
	if c.AWS.Profile != "op" || !c.AWS.OK || c.GCP.ProjectID != "alpha-sandbox" {
		t.Fatalf("%+v", c)
	}
}

func TestAJobWithNoStepsYetStillHasAList(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := newHandler(t, Config{Actions: Actions{GCPSignIn: func(ctx context.Context, _ connect.Progress) error { <-release; return nil }}})
	var id struct{ ID string }
	_ = json.Unmarshal(post(h, "/api/connect/gcp/signin", `{}`, nil).Body.Bytes(), &id)
	if body := get(h, "/api/jobs/"+id.ID, true).Body.String(); !strings.Contains(body, `"steps":[]`) {
		t.Fatalf("steps must be an empty list, not null: %s", body)
	}
}

func TestFindRoleSaysWhenTheSignInMayNotManageTheStack(t *testing.T) {
	h := newHandler(t, Config{Actions: Actions{AWSFindRole: func(context.Context, string, string) (*connect.RoleStack, error) {
		return nil, connect.ErrSetupDenied
	}}})
	var out struct {
		Found, Denied bool
		Error         string
	}
	_ = json.Unmarshal(post(h, "/api/connect/aws/role", `{"profile":"op"}`, nil).Body.Bytes(), &out)
	if out.Found || !out.Denied || out.Error == "" {
		t.Fatalf("%+v", out)
	}
	var pol struct{ Policy string }
	w := post(h, "/api/connect/aws/setup-policy", `{"account":"123456789012"}`, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &pol)
	if w.Code != 200 || !strings.Contains(pol.Policy, "stack/hermes-discovery-role/*") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := post(h, "/api/connect/aws/setup-policy", `{"account":"12; drop"}`, nil); w.Code != 400 {
		t.Fatalf("bad account: %d", w.Code)
	}
}
