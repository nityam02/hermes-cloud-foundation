// Package fakecloud is a pretend AWS and Google Cloud for working on the Hermes UI without any
// account: sign-ins, the role stack, checks, API enabling and discovery all succeed (or fail, if
// asked) after short delays. `make ui-dev` serves the UI over it. Nothing here calls a network.
package fakecloud

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/connect"
	"github.com/nityam02/hermes-cloud-foundation/internal/uiserver"
)

// World is the pretend state of both clouds.
type World struct {
	mu        sync.Mutex
	signedIn  map[string]bool
	newProfs  []string
	role      *connect.RoleStack
	gcloud    bool
	gcpSigned bool
	apis      bool
	billing   bool
	// Delay scales every pretend wait.
	Delay time.Duration
	// DemoDir holds inventory.json, assessment.json and plan.json for the pretend discovery.
	DemoDir string
}

// New returns a fresh machine: one IAM-user profile not signed in, a root profile, no gcloud.
func New(delay time.Duration, demoDir string) *World {
	return &World{signedIn: map[string]bool{"hermes-demo": true}, billing: true, Delay: delay, DemoDir: demoDir}
}

func (w *World) wait(ctx context.Context, n int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(n) * w.Delay):
		return nil
	}
}

const account = "111122223333"

// Actions wires the pretend clouds to the UI server.
func (w *World) Actions() uiserver.Actions {
	return uiserver.Actions{
		AWSProfiles: func() []connect.AWSProfile {
			w.mu.Lock()
			defer w.mu.Unlock()
			out := []connect.AWSProfile{{Name: "default", Kind: "other"}, {Name: "hermes-demo", Kind: "login"}, {Name: "hermes-op", Kind: "login"}}
			for _, n := range w.newProfs {
				out = append(out, connect.AWSProfile{Name: n, Kind: "login"})
			}
			return out
		},
		AWSCLI: func(context.Context) connect.AWSCLI {
			return connect.AWSCLI{Installed: true, Version: "2.35.4", Login: true}
		},
		AWSIdentity: func(ctx context.Context, profile, _ string) (string, string, error) {
			w.mu.Lock()
			ok := w.signedIn[profile]
			w.mu.Unlock()
			_ = w.wait(ctx, 1)
			switch {
			case !ok:
				return "", "", errors.New("failed to refresh cached credentials: unable to load a existing login session")
			case profile == "hermes-demo":
				return "arn:aws:iam::" + account + ":root", account, nil
			}
			return "arn:aws:iam::" + account + ":user/" + profile, account, nil
		},
		AWSSignIn: func(ctx context.Context, req connect.AWSSignInRequest, p connect.Progress) error {
			p.Step("signin", "running", "Sign in to the AWS console in the browser tab that opened, then choose this session.")
			p.Prompt("https://"+req.Region+".signin.aws.amazon.com/v1/authorize?client_id=pretend", "")
			p.Line("Attempting to open your default browser.")
			if err := w.wait(ctx, 6); err != nil {
				return errors.New("sign-in cancelled")
			}
			w.mu.Lock()
			w.signedIn[req.Profile] = true
			known := req.Profile == "default" || req.Profile == "hermes-demo" || req.Profile == "hermes-op"
			for _, n := range w.newProfs {
				known = known || n == req.Profile
			}
			if !known {
				w.newProfs = append(w.newProfs, req.Profile)
			}
			w.mu.Unlock()
			p.Step("signin", "done", "Signed in.")
			return nil
		},
		AWSFindRole: func(ctx context.Context, _, _ string) (*connect.RoleStack, error) {
			_ = w.wait(ctx, 1)
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.role == nil {
				return nil, nil
			}
			cp := *w.role
			return &cp, nil
		},
		AWSEnsureRole: func(ctx context.Context, req connect.RoleRequest, p connect.Progress) (connect.RoleStack, error) {
			p.Step("find", "done", "No role yet. Hermes will create it.")
			p.Step("create", "running", "Creating the read-only role. This usually takes under a minute.")
			if err := w.wait(ctx, 3); err != nil {
				return connect.RoleStack{}, err
			}
			p.Step("create", "running", "CloudFormation: create in progress.")
			if err := w.wait(ctx, 3); err != nil {
				return connect.RoleStack{}, err
			}
			p.Step("create", "done", "Created.")
			p.Step("ready", "running", "Waiting for AWS to make the role usable.")
			_ = w.wait(ctx, 2)
			p.Step("ready", "done", "You can assume the role.")
			ext := req.ExternalID
			if len(ext) < 16 {
				ext = connect.NewExternalID()
			}
			st := connect.RoleStack{Status: "CREATE_COMPLETE", RoleARN: "arn:aws:iam::" + account + ":role/HermesDiscoveryReadOnly", ExternalID: ext, TrustedPrincipal: req.Principal}
			w.mu.Lock()
			w.role = &st
			w.mu.Unlock()
			return st, nil
		},
		AWSRemoveRole: func(ctx context.Context, _, _ string, p connect.Progress) error {
			p.Step("delete", "running", "Deleting the stack hermes-discovery-role.")
			if err := w.wait(ctx, 4); err != nil {
				return err
			}
			w.mu.Lock()
			w.role = nil
			w.mu.Unlock()
			p.Step("delete", "done", "Deleted.")
			return nil
		},
		CheckAWS: func(ctx context.Context, req connect.AWSRequest) connect.Result {
			_ = w.wait(ctx, 4)
			return connect.Result{OK: true, Summary: "Connected. Hermes can read this account through the role.", Identity: "arn:aws:iam::" + account + ":user/" + req.Profile, Steps: []connect.Step{
				{ID: "inputs", Title: "Role details look right", Status: connect.Pass, Detail: req.RoleARN},
				{ID: "identity", Title: "Find who you are signed in as", Status: connect.Pass, Detail: "arn:aws:iam::" + account + ":user/" + req.Profile + " (account " + account + ")"},
				{ID: "role", Title: "Assume the read-only role", Status: connect.Pass, Detail: "Assumed with the external ID. The session lasts one hour."},
				{ID: "read", Title: "Read the account", Status: connect.Pass, Detail: "Found 14 resources in " + req.Region + " (9 relationships)."},
				{ID: "access", Title: "Check read access", Status: connect.Pass, Detail: "The role can read every service Hermes inspects."},
			}}
		},

		GCPStatus: func(ctx context.Context) connect.GCPState {
			_ = w.wait(ctx, 1)
			w.mu.Lock()
			defer w.mu.Unlock()
			s := connect.GCPState{GCloud: w.gcloud, Brew: true, SignedIn: w.gcpSigned}
			if w.gcpSigned {
				s.Account = "you@example.com"
			}
			return s
		},
		GCPInstall: func(ctx context.Context, p connect.Progress) error {
			p.Step("install", "running", "Installing the Google Cloud CLI with Homebrew. This takes a few minutes.")
			for _, l := range []string{"==> Downloading https://dl.google.com/dl/cloudsdk/release/google-cloud-cli-darwin-arm.tar.gz", "==> Installing Cask gcloud-cli", "==> Linking Binary 'gcloud' to '/opt/homebrew/bin/gcloud'", "gcloud-cli was successfully installed!"} {
				if err := w.wait(ctx, 2); err != nil {
					return errors.New("install cancelled")
				}
				p.Line(l)
			}
			w.mu.Lock()
			w.gcloud = true
			w.mu.Unlock()
			p.Step("install", "done", "Installed.")
			return nil
		},
		GCPSignIn: func(ctx context.Context, p connect.Progress) error {
			p.Step("signin", "running", "Sign in with Google in the browser tab that opened, and allow access.")
			p.Prompt("https://accounts.google.com/o/oauth2/auth?client_id=pretend", "")
			if err := w.wait(ctx, 6); err != nil {
				return errors.New("sign-in cancelled")
			}
			w.mu.Lock()
			w.gcpSigned = true
			w.mu.Unlock()
			p.Step("signin", "done", "Signed in.")
			return nil
		},
		GCPProjects: func(ctx context.Context) ([]connect.GCPProject, error) {
			_ = w.wait(ctx, 1)
			return []connect.GCPProject{{ID: "landing-zone-dev", Name: "Landing zone dev", Number: "1001"}, {ID: "migration-sandbox-4471", Name: "Migration sandbox", Number: "1002"}}, nil
		},
		GCPEnableAPIs: func(ctx context.Context, project string, p connect.Progress) error {
			p.Step("list", "done", "10 to turn on.")
			p.Step("enable", "running", "Turning on the APIs Hermes uses in "+project+".")
			if err := w.wait(ctx, 5); err != nil {
				return err
			}
			w.mu.Lock()
			w.apis = true
			w.mu.Unlock()
			p.Step("enable", "done", "Turned on 10 APIs.")
			return nil
		},
		CheckGCP: func(ctx context.Context, req connect.GCPRequest) connect.Result {
			_ = w.wait(ctx, 3)
			w.mu.Lock()
			apis := w.apis
			w.mu.Unlock()
			r := connect.Result{Steps: []connect.Step{
				{ID: "inputs", Title: "Project details look right", Status: connect.Pass, Detail: req.ProjectID + " in " + req.Region},
				{ID: "credentials", Title: "Find your Google sign-in", Status: connect.Pass, Detail: "Application Default Credentials found."},
				{ID: "project", Title: "Find the project", Status: connect.Pass, Detail: req.ProjectID + " (number 1002)"},
				{ID: "billing", Title: "Billing is enabled", Status: connect.Pass, Detail: "A billing account is linked."},
			}}
			if apis {
				r.Steps = append(r.Steps, connect.Step{ID: "apis", Title: "Required APIs are enabled", Status: connect.Pass, Detail: "Every API Hermes uses is already enabled."})
			} else {
				r.Steps = append(r.Steps, connect.Step{ID: "apis", Title: "Required APIs are enabled", Status: connect.Fail,
					Detail: "These must be on before Hermes can deploy: cloudresourcemanager.googleapis.com, serviceusage.googleapis.com.",
					Fix:    "Turn them on with the button below, or run `gcloud services enable cloudresourcemanager.googleapis.com serviceusage.googleapis.com --project " + req.ProjectID + "`."})
			}
			r.Steps = append(r.Steps, connect.Step{ID: "permissions", Title: "You can create the resources Hermes deploys", Status: connect.Pass, Detail: "All 14 checked permissions are granted. This is a preflight, not a guarantee."})
			r.OK = apis
			if apis {
				r.Summary = "Connected. Hermes can see this project."
			} else {
				r.Summary = "Hermes cannot use this project yet."
			}
			return r
		},

		Discover: func(ctx context.Context, _ uiserver.DiscoverRequest, report connect.Reporter) (uiserver.Documents, error) {
			for _, s := range [][2]string{{"discover", "Found 14 resources of 9 types."}, {"assess", "Portability score 71."}, {"plan", "5 phases, 3 approval gates."}} {
				report(s[0], "running", "")
				if err := w.wait(ctx, 3); err != nil {
					return uiserver.Documents{}, err
				}
				report(s[0], "done", s[1])
			}
			var docs uiserver.Documents
			var err error
			read := func(name string) []byte {
				b, e := os.ReadFile(filepath.Join(w.DemoDir, name))
				if e != nil {
					err = fmt.Errorf("fakecloud: %w", e)
				}
				return b
			}
			docs.Inventory, docs.Assessment, docs.Plan = read("inventory.json"), read("assessment.json"), read("plan.json")
			return docs, err
		},
	}
}
