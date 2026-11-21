package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/nityam02/hermes-cloud-foundation/internal/execution"
	"github.com/nityam02/hermes-cloud-foundation/internal/ui"
)

func (app *App) detectEngine(ctx context.Context, preference string) (*execution.Engine, error) {
	if app.DetectEngine != nil {
		return app.DetectEngine(ctx, preference)
	}
	eng, err := execution.Detect(ctx, preference)
	if err != nil {
		return nil, err
	}
	eng.Stdout, eng.Stderr = app.Stderr, app.Stderr // engine progress goes to stderr; data to stdout
	return eng, nil
}

func (app *App) getenv(k string) string {
	if app.Getenv != nil {
		return app.Getenv(k)
	}
	return os.Getenv(k)
}

func (app *App) lookPath(name string) (string, error) {
	if app.LookPath != nil {
		return app.LookPath(name)
	}
	return exec.LookPath(name)
}

func (app *App) now() time.Time {
	if app.Now != nil {
		return app.Now()
	}
	return time.Now()
}

func (app *App) interactive() bool {
	if app.Interactive != nil {
		return app.Interactive()
	}
	return ui.IsTerminal(app.Stdin) && ui.IsTerminal(app.Stdout)
}

func (app *App) runTool(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if app.RunTool != nil {
		return app.RunTool(ctx, name, args...)
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// userName returns "<os user>" or "<os user> (<git user.name>)". Never a credential.
func (app *App) userName() string {
	if app.UserName != nil {
		return app.UserName()
	}
	name := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "config", "--get", "user.name").Output(); err == nil {
		if g := strings.TrimSpace(string(out)); g != "" {
			name += " (" + g + ")"
		}
	}
	return name
}
