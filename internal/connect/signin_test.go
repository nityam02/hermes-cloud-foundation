//go:build unix

package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a Progress that keeps everything it is told.
type recorder struct {
	mu          sync.Mutex
	steps       map[string]string
	links       []string
	codes       []string
	lines       []string
	stepDetails []string
}

func newRecorder() *recorder { return &recorder{steps: map[string]string{}} }

func (r *recorder) Step(name, status, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps[name] = status
	r.stepDetails = append(r.stepDetails, name+":"+status+":"+detail)
}
func (r *recorder) Prompt(link, code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if link != "" {
		r.links = append(r.links, link)
	}
	if code != "" {
		r.codes = append(r.codes, code)
	}
}
func (r *recorder) Line(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, s)
}

// fakeTools puts shell scripts named like the real CLIs in a directory and makes FindTool use only
// that directory. Each script appends its arguments to $HERMES_FAKE_LOG.
func fakeTools(t *testing.T, scripts map[string]string) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "calls.log")
	for name, body := range scripts {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$(basename \"$0\") $*\" >> \"$HERMES_FAKE_LOG\"\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HERMES_FAKE_LOG", log)
	oldLook, oldDirs := lookPath, toolDirs
	lookPath = func(name string) (string, error) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", exec.ErrNotFound
	}
	toolDirs = func() []string { return nil }
	t.Cleanup(func() { lookPath, toolDirs = oldLook, oldDirs })
	return dir, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func awsConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config"), []byte(body), 0o600)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
}

const fakeAWS = `case "$1" in
  --version) echo "aws-cli/${FAKE_AWS_VERSION:-2.35.4} Python/3.14 Darwin/23 exe/arm64" ;;
  login) echo "Attempting to open your default browser."
         echo "If the browser does not open, open the following URL:"
         echo "https://us-east-1.signin.aws.amazon.com/v1/authorize?response_type=code&client_id=x"
         echo "also printed https://evil.example/phish" ;;
  sso) echo "Browser will not be automatically opened."
       echo "Please visit the following URL: https://device.sso.us-east-1.amazonaws.com/"
       echo "Then enter the code:"
       echo "WXYZ-ABCD" ;;
  configure) ;;
esac
exit ${FAKE_EXIT:-0}
`

func TestSignInLinkOnlyTrustsProviderHosts(t *testing.T) {
	for line, want := range map[string]string{
		"open https://us-east-1.signin.aws.amazon.com/v1/authorize?a=b in a browser": "https://us-east-1.signin.aws.amazon.com/v1/authorize?a=b",
		"visit https://device.sso.eu-north-1.amazonaws.com/.":                        "https://device.sso.eu-north-1.amazonaws.com/",
		"Go to https://accounts.google.com/o/oauth2/auth?client_id=1&scope=x":        "https://accounts.google.com/o/oauth2/auth?client_id=1&scope=x",
		"https://my.awsapps.com/start":                                               "https://my.awsapps.com/start",
		"see https://evil.example/aws.amazon.com":                                    "",
		"http://signin.aws.amazon.com/plain-http":                                    "",
		"https://signin.aws.amazon.com.evil.example/x":                               "",
		"https://accounts.google.com.evil.example/":                                  "",
	} {
		if got := SignInLink(line); got != want {
			t.Errorf("%q: got %q want %q", line, got, want)
		}
	}
	if DeviceCode("Then enter the code: QWER-TYUI") != "QWER-TYUI" || DeviceCode("no code here") != "" {
		t.Error("device code")
	}
}

func TestAWSSignInRunsConsoleLoginAndPassesOnTheLink(t *testing.T) {
	_, log := fakeTools(t, map[string]string{"aws": fakeAWS})
	awsConfig(t, "[profile hermes-op]\nlogin_session = arn:aws:iam::123456789012:user/op\n")
	rec := newRecorder()
	if err := AWSSignIn(context.Background(), AWSSignInRequest{Profile: "hermes-op", Region: "eu-north-1"}, rec); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if got[len(got)-1] != "aws login --profile hermes-op --region eu-north-1" {
		t.Fatalf("calls: %v", got)
	}
	if len(rec.links) != 1 || !strings.HasPrefix(rec.links[0], "https://us-east-1.signin.aws.amazon.com/") {
		t.Fatalf("only the AWS link may be passed on: %v", rec.links)
	}
	if rec.steps["signin"] != "done" {
		t.Fatalf("%v", rec.steps)
	}
}

func TestAWSSignInCreatesANewProfileFirst(t *testing.T) {
	_, log := fakeTools(t, map[string]string{"aws": fakeAWS})
	awsConfig(t, "[default]\nregion = us-east-1\n")
	if err := AWSSignIn(context.Background(), AWSSignInRequest{Profile: "hermes", Region: "us-east-1"}, newRecorder()); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if got[0] != "aws configure set region us-east-1 --profile hermes" || got[len(got)-1] != "aws login --profile hermes --region us-east-1" {
		t.Fatalf("calls: %v", got)
	}
}

func TestAWSSignInUsesSSOForIdentityCenterProfiles(t *testing.T) {
	_, log := fakeTools(t, map[string]string{"aws": fakeAWS})
	awsConfig(t, "[profile work]\nsso_session = corp\n[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\n")
	rec := newRecorder()
	if err := AWSSignIn(context.Background(), AWSSignInRequest{Profile: "work"}, rec); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); len(got) != 1 || got[0] != "aws sso login --profile work" {
		t.Fatalf("calls: %v", got)
	}
	if len(rec.codes) != 1 || rec.codes[0] != "WXYZ-ABCD" {
		t.Fatalf("the device code must be shown: %v", rec.codes)
	}
}

func TestAWSSignInRefusals(t *testing.T) {
	_, log := fakeTools(t, map[string]string{"aws": fakeAWS})
	awsConfig(t, "[profile keys]\naws_access_key_id = AKIAEXAMPLE\n[profile chain]\nrole_arn = arn\nsource_profile = keys\n")
	for _, tc := range []struct {
		req  AWSSignInRequest
		want string
	}{
		{AWSSignInRequest{Profile: "bad name; rm -rf"}, "profile name"},
		{AWSSignInRequest{Profile: "--help"}, "profile name"},
		{AWSSignInRequest{Profile: "ok", Region: "nowhere"}, "region"},
		{AWSSignInRequest{Profile: "keys"}, "access keys"},
		{AWSSignInRequest{Profile: "chain"}, "another profile"},
	} {
		err := AWSSignIn(context.Background(), tc.req, newRecorder())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v", tc.req, err)
		}
	}
	if got := calls(t, log); len(got) != 0 {
		t.Fatalf("nothing may run for refused requests: %v", got)
	}
}

func TestAWSSignInNeedsAModernCLI(t *testing.T) {
	fakeTools(t, map[string]string{"aws": fakeAWS})
	awsConfig(t, "[profile p]\nlogin_session = x\n")
	t.Setenv("FAKE_AWS_VERSION", "2.31.9")
	err := AWSSignIn(context.Background(), AWSSignInRequest{Profile: "p"}, newRecorder())
	if err == nil || !strings.Contains(err.Error(), "2.32") || !strings.Contains(err.Error(), "2.31.9") {
		t.Fatalf("%v", err)
	}
	if cli := FindAWSCLI(context.Background()); !cli.Installed || cli.Login || cli.Version != "2.31.9" {
		t.Fatalf("%+v", cli)
	}
}

func TestAWSSignInWithoutTheCLI(t *testing.T) {
	fakeTools(t, nil)
	awsConfig(t, "")
	err := AWSSignIn(context.Background(), AWSSignInRequest{Profile: "p"}, newRecorder())
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
	if FindAWSCLI(context.Background()).Installed {
		t.Fatal("no CLI")
	}
}

func TestSignInFailureShowsWhatTheToolSaid(t *testing.T) {
	fakeTools(t, map[string]string{"aws": fakeAWS})
	awsConfig(t, "[profile p]\nlogin_session = x\n")
	t.Setenv("FAKE_EXIT", "1")
	// --version also exits 1 here, so the version check fails first; the message must still help.
	err := AWSSignIn(context.Background(), AWSSignInRequest{Profile: "p"}, newRecorder())
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunToolCancelStopsTheWholeProcessGroup(t *testing.T) {
	dir, _ := fakeTools(t, map[string]string{"slow": "sleep 30 &\nwait\n"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunTool(ctx, filepath.Join(dir, "slow"), nil, func(string) {}) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the tool")
	}
}

func TestGCPSignInAndInstall(t *testing.T) {
	dir, log := fakeTools(t, map[string]string{
		"brew": `if [ "$1" = install ]; then printf '#!/bin/sh\necho "gcloud $*" >> "$HERMES_FAKE_LOG"\necho "Your browser has been opened to visit:"\necho "    https://accounts.google.com/o/oauth2/auth?response_type=code&client_id=1"\n' > "$(dirname "$0")/gcloud"; chmod +x "$(dirname "$0")/gcloud"; echo "==> Installing Cask gcloud-cli"; fi`,
	})
	if err := GCPSignIn(context.Background(), newRecorder()); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("sign-in without gcloud: %v", err)
	}
	rec := newRecorder()
	if err := InstallGCloud(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gcloud")); err != nil || rec.steps["install"] != "done" {
		t.Fatalf("install: %v %v", err, rec.steps)
	}
	rec = newRecorder()
	if err := GCPSignIn(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	got := calls(t, log)
	if got[0] != "brew install --cask gcloud-cli" || got[len(got)-1] != "gcloud auth application-default login" {
		t.Fatalf("calls: %v", got)
	}
	if len(rec.links) != 1 || !strings.HasPrefix(rec.links[0], "https://accounts.google.com/") {
		t.Fatalf("links: %v", rec.links)
	}
	// Already installed: nothing runs.
	before := len(calls(t, log))
	if err := InstallGCloud(context.Background(), newRecorder()); err != nil || len(calls(t, log)) != before {
		t.Fatal("install must be a no-op when gcloud is there")
	}
}

func TestInstallGCloudWithoutHomebrew(t *testing.T) {
	fakeTools(t, nil)
	if err := InstallGCloud(context.Background(), newRecorder()); err == nil || !strings.Contains(err.Error(), "cloud.google.com/sdk") {
		t.Fatalf("%v", err)
	}
}

// fakeStack is a CloudFormation stack that moves through one *_IN_PROGRESS describe before settling.
type fakeStack struct {
	cur      *RoleStack
	next     *RoleStack // what the stack becomes after the busy describe; Status "gone" means deleted
	calls    []string
	params   map[string]string
	body     string
	onCreate string // final status of a create (default CREATE_COMPLETE)
	changed  bool   // whether an update changes anything
	err      error
}

func (f *fakeStack) Describe(context.Context) (*RoleStack, error) {
	f.calls = append(f.calls, "describe")
	if f.err != nil {
		return nil, f.err
	}
	if f.cur != nil && stackBusy(f.cur.Status) && f.next != nil {
		c := *f.cur
		f.cur, f.next = f.next, nil
		if f.cur.Status == "gone" {
			f.cur = nil
		}
		return &c, nil
	}
	if f.cur == nil {
		return nil, nil
	}
	c := *f.cur
	return &c, nil
}

func (f *fakeStack) Create(_ context.Context, body string, params map[string]string) error {
	f.calls = append(f.calls, "create")
	f.body, f.params = body, params
	final := f.onCreate
	if final == "" {
		final = "CREATE_COMPLETE"
	}
	f.cur = &RoleStack{Status: "CREATE_IN_PROGRESS"}
	f.next = &RoleStack{Status: final, ExternalID: params["ExternalId"], TrustedPrincipal: params["TrustedPrincipalArn"]}
	if final == "CREATE_COMPLETE" {
		f.next.RoleARN = "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly"
	}
	return nil
}

func (f *fakeStack) Update(_ context.Context, body string, params map[string]string) (bool, error) {
	f.calls = append(f.calls, "update")
	f.body, f.params = body, params
	if !f.changed {
		return false, nil
	}
	next := *f.cur
	next.Status = "UPDATE_COMPLETE"
	next.TrustedPrincipal = params["TrustedPrincipalArn"]
	if params["ExternalId"] != "" {
		next.ExternalID = params["ExternalId"]
	}
	f.cur, f.next = &RoleStack{Status: "UPDATE_IN_PROGRESS"}, &next
	return true, nil
}

func (f *fakeStack) Delete(context.Context) error {
	f.calls = append(f.calls, "delete")
	f.cur, f.next = &RoleStack{Status: "DELETE_IN_PROGRESS"}, &RoleStack{Status: "gone"}
	return nil
}

func (f *fakeStack) FailureReason(context.Context) string {
	return "Resource creation cancelled: iam:CreateRole denied"
}

const me = "arn:aws:iam::123456789012:user/op"

func TestEnsureRoleCreatesTheStackFromTheHermesTemplate(t *testing.T) {
	f := &fakeStack{}
	rec := newRecorder()
	st, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me, ExternalID: goodExt}, rec, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if st.RoleARN == "" || st.ExternalID != goodExt || f.params["TrustedPrincipalArn"] != me {
		t.Fatalf("%+v %v", st, f.params)
	}
	if f.body != string(RoleTemplate()) {
		t.Fatal("the stack must be made from the Hermes template, unchanged")
	}
	if strings.Join(f.calls, ",") != "describe,create,describe,describe" || rec.steps["create"] != "done" {
		t.Fatalf("calls %v steps %v", f.calls, rec.steps)
	}
	// No external ID supplied: Hermes generates one.
	f = &fakeStack{}
	st, err = EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond)
	if err != nil || !strings.HasPrefix(st.ExternalID, "hermes-") || len(st.ExternalID) < 16 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestEnsureRoleUpdatesAnExistingStackAndKeepsItsExternalID(t *testing.T) {
	existing := &RoleStack{Status: "CREATE_COMPLETE", RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", ExternalID: "hermes-existing-0000000000", TrustedPrincipal: "arn:aws:iam::123456789012:user/old"}
	f := &fakeStack{cur: existing, changed: true}
	st, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me, ExternalID: "hermes-new-ignored-0000000"}, newRecorder(), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if st.ExternalID != "hermes-existing-0000000000" || st.TrustedPrincipal != me {
		t.Fatalf("%+v", st)
	}
	if f.params["ExternalId"] != "" || f.params["RoleName"] != "" {
		t.Fatalf("an update must keep the previous external ID and role name: %v", f.params)
	}
	// Nothing to change: no wait, same answer.
	f = &fakeStack{cur: &RoleStack{Status: "UPDATE_COMPLETE", RoleARN: existing.RoleARN, ExternalID: existing.ExternalID}}
	if _, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond); err != nil || strings.Join(f.calls, ",") != "describe,update" {
		t.Fatalf("%v %v", err, f.calls)
	}
}

func TestEnsureRoleReplacesAFailedAttempt(t *testing.T) {
	f := &fakeStack{cur: &RoleStack{Status: "ROLLBACK_COMPLETE"}}
	if _, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.Join(f.calls, ","), "describe,delete,describe,describe,create") {
		t.Fatalf("%v", f.calls)
	}
}

func TestEnsureRoleExplainsFailuresAndRefusesRoot(t *testing.T) {
	f := &fakeStack{onCreate: "ROLLBACK_COMPLETE"}
	_, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "iam:CreateRole denied") {
		t.Fatalf("%v", err)
	}
	f = &fakeStack{}
	if _, err := EnsureRole(context.Background(), f, RoleRequest{Principal: "arn:aws:iam::123456789012:root"}, newRecorder(), time.Millisecond); err == nil || len(f.calls) != 0 {
		t.Fatalf("root must be refused before any call: %v %v", err, f.calls)
	}
	f = &fakeStack{cur: &RoleStack{Status: "UPDATE_ROLLBACK_FAILED"}}
	if _, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond); err == nil || !strings.Contains(err.Error(), "will not change") {
		t.Fatalf("a broken stack must be left alone: %v", err)
	}
	f = &fakeStack{err: errors.New("api error AccessDenied: not authorized to perform cloudformation:DescribeStacks")}
	if _, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond); !errors.Is(err, ErrSetupDenied) {
		t.Fatalf("%v", err)
	}
}

func TestRemoveRole(t *testing.T) {
	f := &fakeStack{cur: &RoleStack{Status: "CREATE_COMPLETE"}}
	rec := newRecorder()
	if err := RemoveRole(context.Background(), f, rec, time.Millisecond); err != nil || rec.steps["delete"] != "done" || f.cur != nil {
		t.Fatalf("%v %v %+v", err, rec.steps, f.cur)
	}
	f = &fakeStack{}
	if err := RemoveRole(context.Background(), f, newRecorder(), time.Millisecond); err != nil || strings.Join(f.calls, ",") != "describe" {
		t.Fatalf("nothing to remove: %v %v", err, f.calls)
	}
}

func TestWaitUsableRetriesOnlyWhileIAMPropagates(t *testing.T) {
	n := 0
	assume := func(context.Context, AWSRequest) error {
		n++
		if n < 3 {
			return errors.New("operation error STS: AssumeRole, api error AccessDenied: not authorized to perform: sts:AssumeRole")
		}
		return nil
	}
	if err := WaitUsable(context.Background(), AWSRequest{}, newRecorder(), assume, 5, time.Millisecond); err != nil || n != 3 {
		t.Fatalf("%v after %d tries", err, n)
	}
	n = 0
	other := func(context.Context, AWSRequest) error {
		n++
		return errors.New("ExpiredToken: the security token included in the request is expired")
	}
	if err := WaitUsable(context.Background(), AWSRequest{Profile: "p"}, newRecorder(), other, 5, time.Millisecond); err == nil || n != 1 {
		t.Fatalf("other errors must not be retried: %v after %d", err, n)
	}
}

// fakeGoogleAPI serves the Google endpoints the sign-in, project list and API enabling use.
func fakeGoogleAPI(t *testing.T, enabled map[string]bool) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	opPolls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.Method+" "+r.URL.Path+" project="+r.Header.Get("X-Goog-User-Project"))
		switch {
		case r.URL.Path == "/userinfo":
			_, _ = w.Write([]byte(`{"email":"op@example.com"}`))
		case r.URL.Path == "/v1/projects" && r.URL.Query().Get("pageToken") == "":
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"zeta-sandbox","name":"Zeta","projectNumber":"2"},{"projectId":"alpha-sandbox","name":"alpha","projectNumber":"1"}],"nextPageToken":"p2"}`))
		case r.URL.Path == "/v1/projects":
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"beta-sandbox","name":"Beta","projectNumber":"3"}]}`))
		case strings.HasSuffix(r.URL.Path, "/services") && r.Method == "GET":
			var list []string
			for a, on := range enabled {
				if on {
					list = append(list, fmt.Sprintf(`{"config":{"name":%q}}`, a))
				}
			}
			_, _ = w.Write([]byte(`{"services":[` + strings.Join(list, ",") + `]}`))
		case strings.HasSuffix(r.URL.Path, "/services:batchEnable"):
			var body struct{ ServiceIds []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, a := range body.ServiceIds {
				enabled[a] = true
			}
			_, _ = w.Write([]byte(`{"name":"operations/acf.p2-1","done":false}`))
		case r.URL.Path == "/v1/operations/acf.p2-1":
			opPolls++
			_, _ = fmt.Fprintf(w, `{"name":"operations/acf.p2-1","done":%v}`, opPolls > 1)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func googleDeps(srv *httptest.Server) GCPDeps {
	return GCPDeps{
		HTTP:    func(context.Context) (*http.Client, error) { return srv.Client(), nil },
		CRMBase: srv.URL, UsageBase: srv.URL, BillingBase: srv.URL, UserinfoURL: srv.URL + "/userinfo",
	}
}

func TestGCPStatusProjectsAndEnablingAPIs(t *testing.T) {
	enabled := map[string]bool{"cloudresourcemanager.googleapis.com": true, "serviceusage.googleapis.com": true, "run.googleapis.com": true}
	srv, seen := fakeGoogleAPI(t, enabled)
	d := googleDeps(srv)

	st := GCPStatus(context.Background(), d)
	if !st.SignedIn || st.Account != "op@example.com" {
		t.Fatalf("%+v", st)
	}
	projects, err := GCPProjects(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "alpha-sandbox,beta-sandbox,zeta-sandbox" {
		t.Fatalf("all pages, sorted by name: %v", ids)
	}

	missing, err := MissingGCPAPIs(context.Background(), d, "alpha-sandbox")
	if err != nil || len(missing) != len(GCPRecommendedAPIs)-1 {
		t.Fatalf("%v %v", missing, err)
	}
	rec := newRecorder()
	if err := EnableGCPAPIs(context.Background(), d, "alpha-sandbox", rec, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if rec.steps["enable"] != "done" {
		t.Fatalf("%v", rec.steps)
	}
	if missing, _ := MissingGCPAPIs(context.Background(), d, "alpha-sandbox"); len(missing) != 0 {
		t.Fatalf("still missing %v", missing)
	}
	for _, s := range *seen {
		if strings.Contains(s, "/services") && !strings.HasSuffix(s, "project=alpha-sandbox") {
			t.Errorf("Service Usage calls must bill quota to the project: %s", s)
		}
	}
	if err := EnableGCPAPIs(context.Background(), d, "Not A Project", newRecorder(), time.Millisecond); err == nil {
		t.Fatal("invalid project IDs must be refused")
	}
}

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestGCPStatusWithoutOrWithAnExpiredSignIn(t *testing.T) {
	fakeTools(t, nil)
	none := GCPDeps{HTTP: func(context.Context) (*http.Client, error) {
		return nil, errors.New("could not find default credentials")
	}}
	if st := GCPStatus(context.Background(), none); st.SignedIn || st.GCloud {
		t.Fatalf("%+v", st)
	}
	expired := GCPDeps{UserinfoURL: "https://example.invalid/userinfo", HTTP: func(context.Context) (*http.Client, error) {
		return &http.Client{Transport: failingTransport{errors.New(`oauth2: "invalid_grant" "Token has been expired or revoked."`)}}, nil
	}}
	if st := GCPStatus(context.Background(), expired); st.SignedIn || !strings.Contains(st.Problem, "expired") {
		t.Fatalf("%+v", st)
	}
}

func TestEnsureRoleGivesAHiddenExternalIDANewValue(t *testing.T) {
	// Stacks made from older templates hid the external ID (NoEcho): Describe cannot return it.
	f := &fakeStack{cur: &RoleStack{Status: "UPDATE_COMPLETE", RoleARN: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", TrustedPrincipal: me}, changed: true}
	st, err := EnsureRole(context.Background(), f, RoleRequest{Principal: me}, newRecorder(), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.params["ExternalId"], "hermes-") || f.params["RoleName"] != "" {
		t.Fatalf("a hidden external ID must be replaced, the role name kept: %v", f.params)
	}
	if st.ExternalID != f.params["ExternalId"] {
		t.Fatalf("the new external ID must be returned: %+v", st)
	}
}

func TestSetupPolicyIsScopedToTheStackAndTheRole(t *testing.T) {
	var doc struct {
		Statement []struct {
			Action   []string
			Resource string
		}
	}
	if err := json.Unmarshal(SetupPolicy("123456789012"), &doc); err != nil || len(doc.Statement) != 2 {
		t.Fatalf("%v %+v", err, doc)
	}
	if doc.Statement[0].Resource != "arn:aws:cloudformation:*:123456789012:stack/hermes-discovery-role/*" || doc.Statement[1].Resource != "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly" {
		t.Fatalf("%+v", doc)
	}
	for _, st := range doc.Statement {
		for _, a := range st.Action {
			if strings.Contains(a, "*") {
				t.Errorf("no wildcard actions: %s", a)
			}
		}
	}
	if !errors.Is(explainStackErr(errors.New("api error AccessDenied: not authorized to perform: cloudformation:DescribeStacks")), ErrSetupDenied) {
		t.Fatal("access denied must map to ErrSetupDenied")
	}
}
