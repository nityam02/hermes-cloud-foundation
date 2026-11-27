package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/nityam02/hermes-cloud-foundation/internal/connect"
	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
	"github.com/nityam02/hermes-cloud-foundation/internal/migration"
	"github.com/nityam02/hermes-cloud-foundation/internal/uiserver"
)

func init() { Register(newUICmd) }

func newUICmd(app *App) *cobra.Command {
	var inv, assess, plan, addr string
	var open bool
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the Hermes UI: connect AWS and GCP, check access, discover, assess and plan",
		Long: `Serve a local web UI over the same code as the CLI. It guides you through connecting AWS (a
read-only role with an external ID that Hermes generates) and Google Cloud (Application Default
Credentials), checks both, then runs discovery, assessment and planning, and shows the results.

Security: it only listens on loopback. The link it prints carries a random token that becomes an
HttpOnly cookie; every action also needs a same-origin request and a custom header, and requests
whose Host header is not loopback are refused. It never accepts access keys or key files: it uses
your own sign-in on this machine (aws login / SSO, gcloud auth application-default login).

Discovery, assessment and planning are read-only. This version does not create, change or delete any
cloud resource; deploying and migrating from the UI are designed (ADR 0016) but not implemented.

You can also open existing documents: --inventory, --assessment and --plan.`,
		Example: `  hermes ui --inventory inventory.json --assessment assessment.json --plan plan.yaml`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := uiserver.ValidateAddr(addr); err != nil {
				return Refused("%v", err)
			}
			var docs uiserver.Documents
			var err error
			if inv != "" {
				v, e := inventory.Load(inv)
				if e != nil {
					return e
				}
				if docs.Inventory, err = json.Marshal(v); err != nil {
					return err
				}
			}
			if assess != "" {
				v, e := migration.LoadAssessment(assess)
				if e != nil {
					return e
				}
				if docs.Assessment, err = json.Marshal(v); err != nil {
					return err
				}
			}
			if plan != "" {
				v, e := migration.LoadPlan(plan) // verifies the plan_id against the plan body
				if e != nil {
					return e
				}
				if docs.Plan, err = json.Marshal(v); err != nil {
					return err
				}
			}
			token := uiserver.NewToken()
			h, err := uiserver.New(uiserver.Config{Token: token, Docs: docs, Actions: uiActions(app), Version: app.Build.Version}, nil)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("ui: %w", err)
			}
			url := "http://" + ln.Addr().String() + "/?token=" + token
			fmt.Fprintf(cmd.OutOrStdout(), "Hermes UI is running. Open this link (it carries the session token; keep it private):\n\n  %s\n\nPress Ctrl+C to stop.\n", url)
			if open {
				openBrowser(url)
			}
			srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			go func() {
				<-ctx.Done()
				shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdown)
			}()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&inv, "inventory", "", "inventory JSON from `hermes inventory aws`")
	cmd.Flags().StringVar(&assess, "assessment", "", "assessment JSON from `hermes migrate assess`")
	cmd.Flags().StringVar(&plan, "plan", "", "migration plan from `hermes migrate plan` (its plan_id is verified)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8787", "loopback address to listen on (port 0 picks a free port)")
	cmd.Flags().BoolVar(&open, "open", false, "open the UI in the default browser")
	return cmd
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}

// uiActions wires the UI to the same code the CLI uses.
func uiActions(app *App) uiserver.Actions {
	aws := connect.DefaultAWSDeps()
	gcp := connect.DefaultGCPDeps()
	runs := filepath.Join(".hermes", "ui", "runs")
	return uiserver.Actions{
		AWSProfiles: connect.AWSProfiles,
		AWSIdentity: aws.Identity,
		CheckAWS:    func(ctx context.Context, r connect.AWSRequest) connect.Result { return connect.CheckAWS(ctx, aws, r) },
		CheckGCP:    func(ctx context.Context, r connect.GCPRequest) connect.Result { return connect.CheckGCP(ctx, gcp, r) },
		Discover: func(ctx context.Context, req uiserver.DiscoverRequest, report connect.Reporter) (uiserver.Documents, error) {
			out, err := connect.RunPipeline(ctx, req, runs, app.Build.Version, connect.DiscoverAWS, report)
			if err != nil {
				return uiserver.Documents{}, err
			}
			return uiserver.Documents{Inventory: out.Inventory, Assessment: out.Assessment, Plan: out.Plan}, nil
		},
	}
}
