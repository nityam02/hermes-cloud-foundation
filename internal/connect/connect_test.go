package connect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

const goodRole = "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly"
const goodExt = "hermes-0123456789abcdef"

func step(r Result, id string) Step {
	for _, s := range r.Steps {
		if s.ID == id {
			return s
		}
	}
	return Step{}
}

func awsDeps(arn string, idErr, probeErr error, inv *inventory.Inventory) AWSDeps {
	return AWSDeps{
		Identity: func(context.Context, string, string) (string, string, error) { return arn, "123456789012", idErr },
		Probe: func(context.Context, AWSRequest) (*inventory.Inventory, error) {
			return inv, probeErr
		},
	}
}

func TestAWSHappyPath(t *testing.T) {
	inv := &inventory.Inventory{Resources: make([]inventory.Resource, 3)}
	r := CheckAWS(context.Background(), awsDeps("arn:aws:iam::123456789012:user/hermes-operator", nil, nil, inv), AWSRequest{RoleARN: goodRole, ExternalID: goodExt, Region: "eu-north-1"})
	if !r.OK || step(r, "access").Status != Pass || !strings.Contains(step(r, "read").Detail, "3 resources") {
		t.Fatalf("%+v", r)
	}
	if r.Identity == "" {
		t.Error("identity missing")
	}
}

func TestAWSRootUserIsRefusedWithAFix(t *testing.T) {
	// Found on the first real-account run: AWS never lets root assume a role.
	r := CheckAWS(context.Background(), awsDeps("arn:aws:iam::123456789012:root", nil, nil, &inventory.Inventory{}), AWSRequest{RoleARN: goodRole, ExternalID: goodExt})
	if r.OK || step(r, "identity").Status != Fail || !strings.Contains(step(r, "identity").Fix, "IAM user") {
		t.Fatalf("%+v", r)
	}
	if step(r, "role").Status != Skip {
		t.Error("later steps must be skipped, not attempted")
	}
}

func TestAWSInputValidation(t *testing.T) {
	called := false
	d := AWSDeps{Identity: func(context.Context, string, string) (string, string, error) { called = true; return "", "", nil }}
	for name, req := range map[string]AWSRequest{
		"bad arn":      {RoleARN: "not-an-arn", ExternalID: goodExt},
		"user arn":     {RoleARN: "arn:aws:iam::123456789012:user/x", ExternalID: goodExt},
		"short ext id": {RoleARN: goodRole, ExternalID: "short"},
	} {
		if r := CheckAWS(context.Background(), d, req); r.OK || step(r, "inputs").Status != Fail {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if called {
		t.Error("no AWS call may be made with invalid input")
	}
}

func TestAWSErrorsExplainThemselves(t *testing.T) {
	cases := map[string]struct {
		idErr, probeErr error
		step, want      string
	}{
		"expired login":     {idErr: errors.New("operation error STS: GetCallerIdentity, api error InvalidClientTokenId"), step: "identity", want: "aws login"},
		"expired aws login": {idErr: errors.New("operation error STS: GetCallerIdentity, get identity: get credentials: failed to refresh cached credentials, load login token: token file not found"), step: "identity", want: "aws login"},
		"trust mismatch":    {probeErr: errors.New("operation error STS: AssumeRole, https response error StatusCode: 403, api error AccessDenied: User is not authorized to perform: sts:AssumeRole"), step: "role", want: "trust policy"},
		"root assume":       {probeErr: errors.New("AssumeRole api error AccessDenied: Roles may not be assumed by root accounts."), step: "role", want: "root"},
	}
	for name, c := range cases {
		r := CheckAWS(context.Background(), awsDeps("arn:aws:iam::123456789012:user/u", c.idErr, c.probeErr, nil), AWSRequest{RoleARN: goodRole, ExternalID: goodExt})
		s := step(r, c.step)
		if r.OK || s.Status != Fail || !strings.Contains(s.Detail+s.Fix, c.want) {
			t.Errorf("%s: %+v", name, s)
		}
	}
}

func TestAWSGapsAreAWarningNotAFailure(t *testing.T) {
	inv := &inventory.Inventory{Gaps: []inventory.Gap{{Service: "cloudformation", Operation: "DescribeStacks"}, {Service: "cloudformation", Operation: "DescribeStacks"}}}
	r := CheckAWS(context.Background(), awsDeps("arn:aws:iam::123456789012:user/u", nil, nil, inv), AWSRequest{RoleARN: goodRole, ExternalID: goodExt})
	if !r.OK || step(r, "access").Status != Warn || !strings.Contains(step(r, "access").Detail, "cloudformation:DescribeStacks") {
		t.Fatalf("%+v", r)
	}
}

type gcpOpts struct {
	status    map[string]int
	billing   bool
	apis      []string
	granted   []string
	lifecycle string
	failCreds bool
}

// fakeGoogle serves the four Google APIs the check calls.
func fakeGoogle(t *testing.T, o gcpOpts) (GCPDeps, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	code := func(k string) int {
		if c, ok := o.status[k]; ok {
			return c
		}
		return 200
	}
	mux.HandleFunc("/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, ":testIamPermissions"):
			if c := code("permissions"); c != 200 {
				w.WriteHeader(c)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"permissions": o.granted})
		case strings.HasSuffix(p, "/billingInfo"):
			if c := code("billing"); c != 200 {
				w.WriteHeader(c)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"billingEnabled": o.billing})
		case strings.HasSuffix(p, "/services"):
			if c := code("services"); c != 200 {
				w.WriteHeader(c)
				return
			}
			var svc []map[string]any
			for _, a := range o.apis {
				svc = append(svc, map[string]any{"config": map[string]any{"name": a}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"services": svc})
		default:
			if c := code("project"); c != 200 {
				w.WriteHeader(c)
				return
			}
			lc := o.lifecycle
			if lc == "" {
				lc = "ACTIVE"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "Sandbox", "projectId": "demo-sandbox-1", "projectNumber": "42", "lifecycleState": lc})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return GCPDeps{
		HTTP: func(context.Context) (*http.Client, error) {
			if o.failCreds {
				return nil, errors.New("could not find default credentials")
			}
			return srv.Client(), nil
		},
		CRMBase: srv.URL, UsageBase: srv.URL, BillingBase: srv.URL,
	}, srv
}

func TestGCPHappyPath(t *testing.T) {
	d, _ := fakeGoogle(t, gcpOpts{billing: true, apis: append(append([]string{}, GCPRequiredAPIs...), GCPRecommendedAPIs...), granted: GCPDeployPermissions})
	r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
	if !r.OK {
		t.Fatalf("%+v", r)
	}
	for _, id := range []string{"project", "billing", "apis", "permissions"} {
		if step(r, id).Status != Pass {
			t.Errorf("%s: %+v", id, step(r, id))
		}
	}
}

func TestGCPFailuresExplainThemselves(t *testing.T) {
	good := append(append([]string{}, GCPRequiredAPIs...), GCPRecommendedAPIs...)
	t.Run("no credentials", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{failCreds: true})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if r.OK || !strings.Contains(step(r, "credentials").Fix, "gcloud auth application-default login") || step(r, "project").Status != Skip {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("project not visible", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{status: map[string]int{"project": 403}})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if r.OK || step(r, "project").Status != Fail {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("expired credentials", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{status: map[string]int{"project": 401}})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if r.OK || !strings.Contains(step(r, "project").Fix, "again") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("billing off", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{billing: false, apis: good, granted: GCPDeployPermissions})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if r.OK || step(r, "billing").Status != Fail || !strings.Contains(step(r, "billing").Fix, "billing account") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("bootstrap api off", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{billing: true, apis: []string{"run.googleapis.com"}, granted: GCPDeployPermissions})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if r.OK || !strings.Contains(step(r, "apis").Fix, "gcloud services enable cloudresourcemanager.googleapis.com serviceusage.googleapis.com") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("missing permissions only warn", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{billing: true, apis: good, granted: []string{"resourcemanager.projects.get"}})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if !r.OK || step(r, "permissions").Status != Warn {
			t.Fatalf("discovery and planning work without deploy permissions, so this must warn: %+v", r)
		}
	})
	t.Run("permissions and api listing unavailable only warn", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{billing: true, status: map[string]int{"permissions": 500, "services": 403}})
		r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"})
		if !r.OK || step(r, "permissions").Status != Warn || step(r, "apis").Status != Warn {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("inactive project", func(t *testing.T) {
		d, _ := fakeGoogle(t, gcpOpts{lifecycle: "DELETE_REQUESTED"})
		if r := CheckGCP(context.Background(), d, GCPRequest{ProjectID: "demo-sandbox-1", Region: "europe-north1"}); r.OK {
			t.Fatalf("%+v", r)
		}
	})
}

func TestGCPInputValidationMakesNoCall(t *testing.T) {
	called := false
	d := GCPDeps{HTTP: func(context.Context) (*http.Client, error) { called = true; return nil, errors.New("x") }}
	for _, req := range []GCPRequest{{ProjectID: "My Project", Region: "europe-north1"}, {ProjectID: "123456", Region: "europe-north1"}, {ProjectID: "demo-sandbox-1", Region: ""}, {ProjectID: "", Region: "x"}} {
		if r := CheckGCP(context.Background(), d, req); r.OK || step(r, "inputs").Status != Fail {
			t.Errorf("%+v: %+v", req, r)
		}
	}
	if called {
		t.Error("no Google call may be made with invalid input")
	}
}

func TestAWSProfilesReturnsNamesAndKindsOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := dir + "/config"
	creds := dir + "/credentials"
	secret := "SECRETVALUE-do-not-leak"
	_ = os.WriteFile(cfg, []byte("[default]\nregion=eu-north-1\n[profile work]\nsso_session=x\n[sso-session x]\nsso_start_url=https://x\n[profile hermes-op]\nlogin_session=arn\n[profile chain]\nrole_arn=arn\nsource_profile=work\n# [profile commented]\n"), 0o600)
	_ = os.WriteFile(creds, []byte("[legacy]\naws_access_key_id = AKIAEXAMPLE\naws_secret_access_key="+secret+"\n[default]\n"), 0o600)
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", creds)
	got := AWSProfiles()
	var flat []string
	for _, p := range got {
		flat = append(flat, p.Name+"="+p.Kind)
	}
	want := "chain=role,default=other,hermes-op=login,legacy=keys,work=sso"
	if strings.Join(flat, ",") != want {
		t.Fatalf("got %v want %v", flat, want)
	}
	out, _ := json.Marshal(got)
	if strings.Contains(string(out), secret) || strings.Contains(string(out), "AKIA") {
		t.Fatal("a credential value leaked into the profile list")
	}
}
