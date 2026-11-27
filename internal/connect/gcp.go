package connect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GCPRequest describes the Google Cloud side of a migration.
type GCPRequest struct {
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`
}

// GCPDeployPermissions are checked with testIamPermissions on the project. It is a preflight for the
// identity that will run `hermes apply`, not a guarantee: some permissions depend on the resource.
var GCPDeployPermissions = []string{
	"serviceusage.services.enable",
	"resourcemanager.projects.get",
	"compute.networks.create",
	"iam.serviceAccounts.create",
	"run.services.create",
	"run.services.setIamPolicy",
	"cloudsql.instances.create",
	"storage.buckets.create",
	"pubsub.topics.create",
	"pubsub.subscriptions.create",
	"secretmanager.secrets.create",
	"cloudkms.keyRings.create",
	"logging.logMetrics.create",
	"monitoring.alertPolicies.create",
}

// GCPRequiredAPIs must be enabled before `hermes apply` (the first two cannot be enabled by Terraform
// itself); GCPRecommendedAPIs are enabled by Hermes during apply when the identity may do so.
var (
	GCPRequiredAPIs    = []string{"cloudresourcemanager.googleapis.com", "serviceusage.googleapis.com"}
	GCPRecommendedAPIs = []string{"run.googleapis.com", "sqladmin.googleapis.com", "storage.googleapis.com", "pubsub.googleapis.com",
		"secretmanager.googleapis.com", "cloudkms.googleapis.com", "compute.googleapis.com", "iam.googleapis.com"}
)

// GCPDeps are the Google calls, replaceable in tests.
type GCPDeps struct {
	// HTTP returns a client that authenticates with Application Default Credentials.
	HTTP                            func(ctx context.Context) (*http.Client, error)
	CRMBase, UsageBase, BillingBase string
}

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// DefaultGCPDeps use Application Default Credentials: `gcloud auth application-default login`, a service
// account through impersonation, or workload identity federation. Hermes never accepts a key file path
// from the UI.
func DefaultGCPDeps() GCPDeps {
	return GCPDeps{
		HTTP: func(ctx context.Context) (*http.Client, error) {
			creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
			if err != nil {
				return nil, err
			}
			return &http.Client{Timeout: 30 * time.Second, Transport: &oauth2.Transport{Source: creds.TokenSource, Base: http.DefaultTransport}}, nil
		},
		CRMBase:     "https://cloudresourcemanager.googleapis.com",
		UsageBase:   "https://serviceusage.googleapis.com",
		BillingBase: "https://cloudbilling.googleapis.com",
	}
}

// CheckGCP runs the Google Cloud connection test.
func CheckGCP(ctx context.Context, d GCPDeps, req GCPRequest) Result {
	var r Result
	later := [][2]string{{"project", "Find the project"}, {"billing", "Billing is enabled"}, {"apis", "Required APIs are enabled"}, {"permissions", "You can create the resources Hermes deploys"}}

	if !projectIDPattern.MatchString(req.ProjectID) {
		r.Steps = append(r.Steps, Step{ID: "inputs", Title: "Project details look right", Status: Fail,
			Detail: "A project ID is 6 to 30 lowercase letters, digits or hyphens, and starts with a letter.",
			Fix:    "Use the project ID (not the display name or number) from the Google Cloud console project picker."})
		skipRest(&r, append([][2]string{{"credentials", "Find your Google sign-in"}}, later...), "Fix the project details first.")
		finish(&r, "", "The project details need fixing.")
		return r
	}
	if strings.TrimSpace(req.Region) == "" {
		r.Steps = append(r.Steps, Step{ID: "inputs", Title: "Project details look right", Status: Fail, Detail: "Pick a region, for example europe-north1.", Fix: "Choose where the new resources should live."})
		skipRest(&r, append([][2]string{{"credentials", "Find your Google sign-in"}}, later...), "Fix the project details first.")
		finish(&r, "", "The project details need fixing.")
		return r
	}
	r.Steps = append(r.Steps, Step{ID: "inputs", Title: "Project details look right", Status: Pass, Detail: fmt.Sprintf("%s in %s", req.ProjectID, req.Region)})

	cl, err := d.HTTP(ctx)
	if err != nil {
		r.Steps = append(r.Steps, Step{ID: "credentials", Title: "Find your Google sign-in", Status: Fail,
			Detail: "No Google credentials were found on this machine.",
			Fix:    "Run `gcloud auth application-default login` and sign in with the account that can deploy to this project. Or set up service account impersonation or workload identity federation. Hermes never asks for a key file."})
		skipRest(&r, later, "Sign in first.")
		finish(&r, "", "Hermes could not find your Google sign-in.")
		return r
	}
	r.Steps = append(r.Steps, Step{ID: "credentials", Title: "Find your Google sign-in", Status: Pass, Detail: "Application Default Credentials found."})

	// project
	var proj struct {
		Name, ProjectID, LifecycleState, ProjectNumber string
	}
	code, err := getJSON(ctx, cl, d.CRMBase+"/v1/projects/"+url.PathEscape(req.ProjectID), &proj)
	switch {
	case err != nil || code == 401:
		r.Steps = append(r.Steps, Step{ID: "project", Title: "Find the project", Status: Fail, Detail: "Google rejected the credentials.",
			Fix: "They may have expired. Run `gcloud auth application-default login` again."})
		skipRest(&r, later[1:], "The project must be found first.")
		finish(&r, "", "Google rejected the credentials.")
		return r
	case code == 403 || code == 404:
		r.Steps = append(r.Steps, Step{ID: "project", Title: "Find the project", Status: Fail,
			Detail: "The project was not found, or your sign-in cannot see it.",
			Fix:    "Check the project ID, and that the account you signed in with has access (for example Viewer or Editor) on it."})
		skipRest(&r, later[1:], "The project must be found first.")
		finish(&r, "", "Hermes cannot see this project.")
		return r
	case code != 200:
		r.Steps = append(r.Steps, Step{ID: "project", Title: "Find the project", Status: Fail, Detail: fmt.Sprintf("Google answered with status %d.", code), Fix: "Try again in a minute."})
		skipRest(&r, later[1:], "The project must be found first.")
		finish(&r, "", "Google did not answer as expected.")
		return r
	}
	if proj.LifecycleState != "" && proj.LifecycleState != "ACTIVE" {
		r.Steps = append(r.Steps, Step{ID: "project", Title: "Find the project", Status: Fail, Detail: fmt.Sprintf("The project is %s.", proj.LifecycleState), Fix: "Use an active project."})
		skipRest(&r, later[1:], "The project must be active.")
		finish(&r, "", "The project is not active.")
		return r
	}
	r.Steps = append(r.Steps, Step{ID: "project", Title: "Find the project", Status: Pass, Detail: fmt.Sprintf("%s (number %s)", firstNonEmpty(proj.Name, proj.ProjectID), proj.ProjectNumber)})

	// billing
	var bill struct{ BillingEnabled bool }
	code, _ = getJSON(ctx, cl, d.BillingBase+"/v1/projects/"+url.PathEscape(req.ProjectID)+"/billingInfo", &bill)
	switch {
	case code == 200 && bill.BillingEnabled:
		r.Steps = append(r.Steps, Step{ID: "billing", Title: "Billing is enabled", Status: Pass, Detail: "A billing account is linked."})
	case code == 200:
		r.Steps = append(r.Steps, Step{ID: "billing", Title: "Billing is enabled", Status: Fail, Detail: "The project has no billing account.", Fix: "Link a billing account to the project in the Google Cloud console. Cloud Run, Cloud SQL and most services need it."})
	default:
		r.Steps = append(r.Steps, Step{ID: "billing", Title: "Billing is enabled", Status: Warn, Detail: "Hermes could not read the billing status with this sign-in.", Fix: "Check in the console that billing is linked."})
	}

	// APIs
	enabled, code := enabledAPIs(ctx, cl, d.UsageBase, req.ProjectID)
	if code != 200 {
		r.Steps = append(r.Steps, Step{ID: "apis", Title: "Required APIs are enabled", Status: Warn, Detail: "Hermes could not list the enabled APIs with this sign-in.",
			Fix: "Make sure the Cloud Resource Manager and Service Usage APIs are enabled: `gcloud services enable cloudresourcemanager.googleapis.com serviceusage.googleapis.com`."})
	} else {
		var missingReq, missingRec []string
		for _, a := range GCPRequiredAPIs {
			if !enabled[a] {
				missingReq = append(missingReq, a)
			}
		}
		for _, a := range GCPRecommendedAPIs {
			if !enabled[a] {
				missingRec = append(missingRec, a)
			}
		}
		switch {
		case len(missingReq) > 0:
			r.Steps = append(r.Steps, Step{ID: "apis", Title: "Required APIs are enabled", Status: Fail,
				Detail: "These must be on before Hermes can deploy: " + strings.Join(missingReq, ", ") + ".",
				Fix:    "Run `gcloud services enable " + strings.Join(missingReq, " ") + " --project " + req.ProjectID + "`."})
		case len(missingRec) > 0:
			r.Steps = append(r.Steps, Step{ID: "apis", Title: "Required APIs are enabled", Status: Pass,
				Detail: fmt.Sprintf("The two bootstrap APIs are on. Hermes will enable %d more during apply (%s) if you may enable APIs.", len(missingRec), strings.Join(missingRec, ", "))})
		default:
			r.Steps = append(r.Steps, Step{ID: "apis", Title: "Required APIs are enabled", Status: Pass, Detail: "Every API Hermes uses is already enabled."})
		}
	}

	// permissions
	var tip struct{ Permissions []string }
	body, _ := json.Marshal(map[string]any{"permissions": GCPDeployPermissions})
	code, err = postJSON(ctx, cl, d.CRMBase+"/v1/projects/"+url.PathEscape(req.ProjectID)+":testIamPermissions", body, &tip)
	if err != nil || code != 200 {
		r.Steps = append(r.Steps, Step{ID: "permissions", Title: "You can create the resources Hermes deploys", Status: Warn,
			Detail: "Hermes could not test your permissions.", Fix: "Planning still works. If apply fails with permission errors, give the deploying account the roles listed in docs/security/iam-strategy.md."})
	} else {
		have := map[string]bool{}
		for _, p := range tip.Permissions {
			have[p] = true
		}
		var missing []string
		for _, p := range GCPDeployPermissions {
			if !have[p] {
				missing = append(missing, p)
			}
		}
		sort.Strings(missing)
		if len(missing) == 0 {
			r.Steps = append(r.Steps, Step{ID: "permissions", Title: "You can create the resources Hermes deploys", Status: Pass,
				Detail: fmt.Sprintf("All %d checked permissions are granted. This is a preflight, not a guarantee.", len(GCPDeployPermissions))})
		} else {
			// Reading and planning need little; creating needs these. Warn rather than fail: the user may
			// only want to discover and plan today.
			r.Steps = append(r.Steps, Step{ID: "permissions", Title: "You can create the resources Hermes deploys", Status: Warn,
				Detail: fmt.Sprintf("%d of %d checked permissions are missing: %s.", len(missing), len(GCPDeployPermissions), strings.Join(firstN(missing, 6), ", ")),
				Fix:    "Discovery and planning work without them. Before apply, grant the deploying account the roles in docs/security/iam-strategy.md (Editor plus Project IAM Admin covers a sandbox project)."})
		}
	}
	finish(&r, "Connected. Hermes can see this project.", "Hermes cannot use this project yet.")
	return r
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), fmt.Sprintf("and %d more", len(s)-n))
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func enabledAPIs(ctx context.Context, cl *http.Client, base, project string) (map[string]bool, int) {
	out := map[string]bool{}
	token := ""
	for page := 0; page < 10; page++ {
		u := fmt.Sprintf("%s/v1/projects/%s/services?filter=state:ENABLED&pageSize=200", base, url.PathEscape(project))
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}
		var resp struct {
			Services []struct {
				Config struct{ Name string }
			}
			NextPageToken string
		}
		code, err := getJSON(ctx, cl, u, &resp)
		if err != nil || code != 200 {
			return nil, code
		}
		for _, s := range resp.Services {
			out[s.Config.Name] = true
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	return out, 200
}

func getJSON(ctx context.Context, cl *http.Client, u string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	return doJSON(cl, req, out)
}

func postJSON(ctx context.Context, cl *http.Client, u string, body []byte, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	return doJSON(cl, req, out)
}

func doJSON(cl *http.Client, req *http.Request, out any) (int, error) {
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode == 200 && out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
