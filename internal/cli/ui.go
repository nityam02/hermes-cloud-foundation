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
	"runtime"
	"time"

	"github.com/spf13/cobra"

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
		Short: "Open the read-only Hermes UI for an inventory, assessment and migration plan",
		Long: `Serve a local, read-only web UI that shows the documents Hermes already produces:
the inventory (hermes inventory aws), the assessment (hermes migrate assess) and the migration
plan (hermes migrate plan). It cannot plan, apply, approve or cut over anything.

The UI has no authentication, so it only listens on loopback (127.0.0.1, ::1 or localhost) and
refuses requests whose Host header is not loopback. Nothing leaves your machine.`,
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
			h, err := uiserver.New(docs, nil)
			if err != nil {
				return err
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("ui: %w", err)
			}
			url := "http://" + ln.Addr().String() + "/"
			fmt.Fprintf(cmd.OutOrStdout(), "Hermes UI (read-only) on %s — press Ctrl+C to stop.\n", url)
			if docs.Inventory == nil && docs.Assessment == nil && docs.Plan == nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "No documents given: use --inventory, --assessment and --plan to show your data.")
			}
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
