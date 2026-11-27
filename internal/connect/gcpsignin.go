package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// GCPState is what the page needs to know about Google Cloud on this machine.
type GCPState struct {
	GCloud   bool   `json:"gcloud"`            // the Google Cloud CLI is installed
	Brew     bool   `json:"brew"`              // Homebrew is installed, so Hermes can install the CLI
	SignedIn bool   `json:"signed_in"`         // Application Default Credentials work
	Account  string `json:"account,omitempty"` // the signed-in email, when Google says
	Problem  string `json:"problem,omitempty"` // why a sign-in that exists cannot be used
}

// GCPStatus checks for the CLI and the sign-in. The only call is Google's userinfo endpoint, which also
// proves the sign-in has not expired.
func GCPStatus(ctx context.Context, d GCPDeps) GCPState {
	var s GCPState
	_, s.GCloud = FindTool("gcloud")
	_, s.Brew = FindTool("brew")
	cl, err := d.HTTP(ctx)
	if err != nil {
		return s
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var info struct{ Email string }
	code, err := getJSON(ctx, cl, d.UserinfoURL, &info)
	switch {
	case err != nil && (strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "invalid_rapt") || strings.Contains(err.Error(), "oauth2")):
		s.Problem = "Your Google sign-in has expired or was revoked. Sign in again."
	case err != nil:
		s.Problem = "Hermes could not reach Google to check your sign-in: " + truncate(err.Error(), 200)
	default:
		s.SignedIn = true
		if code == 200 {
			s.Account = info.Email
		}
	}
	return s
}

// GCPSignIn runs `gcloud auth application-default login`, which signs in with Google in the browser
// and saves Application Default Credentials where Google's libraries look for them.
func GCPSignIn(ctx context.Context, p Progress) error {
	gcloud, ok := FindTool("gcloud")
	if !ok {
		return errors.New("the Google Cloud CLI is not installed on this machine. Install it first")
	}
	p.Step("signin", "running", "Sign in with Google in the browser tab that opened, and allow access.")
	if err := signIn(ctx, gcloud, []string{"auth", "application-default", "login"}, false, p); err != nil {
		return err
	}
	p.Step("signin", "done", "Signed in.")
	return nil
}

// InstallGCloud installs the Google Cloud CLI with Homebrew (`brew install --cask gcloud-cli`).
func InstallGCloud(ctx context.Context, p Progress) error {
	if _, ok := FindTool("gcloud"); ok {
		p.Step("install", "done", "The Google Cloud CLI is already installed.")
		return nil
	}
	brew, ok := FindTool("brew")
	if !ok {
		return errors.New("homebrew is not installed, so Hermes cannot install the Google Cloud CLI for you. Install it from https://cloud.google.com/sdk/docs/install, then check again")
	}
	p.Step("install", "running", "Installing the Google Cloud CLI with Homebrew. This takes a few minutes.")
	if err := RunTool(ctx, brew, []string{"install", "--cask", "gcloud-cli"}, p.Line); err != nil {
		if errors.Is(err, context.Canceled) {
			return errors.New("install cancelled")
		}
		return fmt.Errorf("homebrew could not install the Google Cloud CLI: %w", err)
	}
	if _, ok := FindTool("gcloud"); !ok {
		return errors.New("homebrew finished, but Hermes cannot find gcloud. Open a new terminal, check `gcloud --version`, then restart `hermes ui`")
	}
	p.Step("install", "done", "Installed.")
	return nil
}

// GCPProject is one project the signed-in account can see.
type GCPProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
}

// GCPProjects lists the active projects the signed-in account can see (up to 1000).
func GCPProjects(ctx context.Context, d GCPDeps) ([]GCPProject, error) {
	cl, err := d.HTTP(ctx)
	if err != nil {
		return nil, errors.New("not signed in to Google")
	}
	var out []GCPProject
	token := ""
	for page := 0; page < 5; page++ {
		u := d.CRMBase + "/v1/projects?pageSize=200&filter=" + url.QueryEscape("lifecycleState:ACTIVE")
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}
		var resp struct {
			Projects []struct {
				ProjectID, Name, ProjectNumber string
			}
			NextPageToken string
		}
		code, msg, err := callGoogle(ctx, cl, http.MethodGet, u, nil, &resp)
		if err != nil {
			return nil, err
		}
		if code != 200 {
			return nil, fmt.Errorf("google answered %d: %s", code, msg)
		}
		for _, p := range resp.Projects {
			out = append(out, GCPProject{ID: p.ProjectID, Name: p.Name, Number: p.ProjectNumber})
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	sort.Slice(out, func(i, j int) bool {
		if !strings.EqualFold(out[i].Name, out[j].Name) {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// MissingGCPAPIs returns the APIs Hermes uses that are not enabled on the project.
func MissingGCPAPIs(ctx context.Context, d GCPDeps, project string) ([]string, error) {
	cl, err := d.HTTP(ctx)
	if err != nil {
		return nil, errors.New("not signed in to Google")
	}
	enabled, code := enabledAPIs(ctx, withQuotaProject(cl, project), d.UsageBase, project)
	if code != 200 {
		return nil, fmt.Errorf("could not list the enabled APIs (Google answered %d)", code)
	}
	var missing []string
	for _, a := range append(append([]string{}, GCPRequiredAPIs...), GCPRecommendedAPIs...) {
		if !enabled[a] {
			missing = append(missing, a)
		}
	}
	return missing, nil
}

// EnableGCPAPIs turns on the APIs Hermes uses that are off, and waits for Google to finish. Enabling
// an API costs nothing by itself; resources created later do.
func EnableGCPAPIs(ctx context.Context, d GCPDeps, project string, p Progress, poll time.Duration) error {
	if !projectIDPattern.MatchString(project) {
		return errors.New("that is not a Google Cloud project ID")
	}
	p.Step("list", "running", "Checking which APIs are already on.")
	missing, err := MissingGCPAPIs(ctx, d, project)
	if err != nil {
		// Service Usage may itself be off; try to turn on the two bootstrap APIs anyway.
		missing = append([]string{}, GCPRequiredAPIs...)
		p.Step("list", "warn", err.Error()+". Trying the two bootstrap APIs.")
	} else {
		p.Step("list", "done", fmt.Sprintf("%d to turn on.", len(missing)))
	}
	if len(missing) == 0 {
		p.Step("enable", "done", "Every API Hermes uses is already on.")
		return nil
	}
	cl, err := d.HTTP(ctx)
	if err != nil {
		return errors.New("not signed in to Google")
	}
	cl = withQuotaProject(cl, project)
	p.Step("enable", "running", "Turning on "+strings.Join(missing, ", ")+".")
	body, _ := json.Marshal(map[string]any{"serviceIds": missing})
	var op struct {
		Name  string
		Done  bool
		Error *struct{ Message string }
	}
	code, msg, err := callGoogle(ctx, cl, http.MethodPost, d.UsageBase+"/v1/projects/"+url.PathEscape(project)+"/services:batchEnable", body, &op)
	if err != nil {
		return err
	}
	if code != 200 {
		if code == 403 {
			return fmt.Errorf("google refused: %s. You need permission to enable services on this project (for example the Owner, Editor or Service Usage Admin role)", msg)
		}
		return fmt.Errorf("google answered %d: %s", code, msg)
	}
	for !op.Done {
		select {
		case <-ctx.Done():
			return errors.New("stopped waiting; Google may still be turning the APIs on")
		case <-time.After(poll):
		}
		if !strings.HasPrefix(op.Name, "operations/") {
			return fmt.Errorf("unexpected operation name %q", op.Name)
		}
		code, msg, err = callGoogle(ctx, cl, http.MethodGet, d.UsageBase+"/v1/"+op.Name, nil, &op)
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("google answered %d: %s", code, msg)
		}
	}
	if op.Error != nil {
		return fmt.Errorf("google could not turn the APIs on: %s", op.Error.Message)
	}
	p.Step("enable", "done", fmt.Sprintf("Turned on %d APIs.", len(missing)))
	return nil
}

// callGoogle sends one request and returns the status, Google's error message when it failed, and a
// transport error.
func callGoogle(ctx context.Context, cl *http.Client, method, u string, body []byte, out any) (int, string, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	if resp.StatusCode != 200 {
		var e struct {
			Error struct{ Message string }
		}
		_ = json.Unmarshal(data, &e)
		return resp.StatusCode, truncate(firstNonEmpty(e.Error.Message, http.StatusText(resp.StatusCode)), 300), nil
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, "", err
		}
	}
	return resp.StatusCode, "", nil
}
