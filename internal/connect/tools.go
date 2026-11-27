package connect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Hermes never handles a cloud password, key or token itself. To sign in it runs the cloud's own CLI
// (`aws login`, `aws sso login`, `gcloud auth application-default login`), which opens the provider's
// sign-in page in the browser and stores the result where that CLI and the SDKs normally look.

// Progress receives what a long operation is doing, for the person watching it.
type Progress interface {
	// Step reports a named stage: status is running, done, failed or warn.
	Step(name, status, detail string)
	// Prompt passes on what a sign-in needs from the person: a page to open if the browser did not
	// open by itself, and a device code to confirm there.
	Prompt(link, code string)
	// Line passes on one line a tool printed.
	Line(s string)
}

// toolDirs are the usual install locations, checked after PATH because `hermes ui` may start from a
// shell whose PATH misses them. Tests replace it.
var toolDirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/opt/homebrew/bin", "/usr/local/bin",
		"/opt/homebrew/share/google-cloud-sdk/bin", "/usr/local/share/google-cloud-sdk/bin",
		filepath.Join(home, "google-cloud-sdk", "bin"),
	}
}

// lookPath is exec.LookPath; tests replace it.
var lookPath = exec.LookPath

// FindTool returns the full path of a CLI, from PATH or a usual install location.
func FindTool(name string) (string, bool) {
	if p, err := lookPath(name); err == nil {
		return p, true
	}
	for _, dir := range toolDirs() {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// RunTool runs a CLI without a shell and with stdin closed, and passes each line it prints to onLine.
// Cancelling ctx stops the tool and anything it started.
func RunTool(ctx context.Context, path string, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "AWS_PAGER=", "CLOUDSDK_CORE_DISABLE_PROMPTS=1")
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.WaitDelay = 3 * time.Second
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			onLine(strings.TrimRight(sc.Text(), "\r"))
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Wait()
	_ = pw.Close()
	<-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

var (
	linkPattern = regexp.MustCompile(`https://[^\s"'<>\]\)]+`)
	codePattern = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4}\b`)
)

// signInHosts are the only places a sign-in link may point to. Anything else a tool prints is not
// shown as a link.
func signInHost(host string) bool {
	host = strings.ToLower(host)
	if host == "accounts.google.com" {
		return true
	}
	for _, suffix := range []string{".aws.amazon.com", ".amazonaws.com", ".awsapps.com", ".signin.aws"} {
		if strings.HasSuffix(host, suffix) || host == strings.TrimPrefix(suffix, ".") {
			return true
		}
	}
	return false
}

// SignInLink returns the first link in a line that points at an AWS or Google sign-in host.
func SignInLink(line string) string {
	for _, m := range linkPattern.FindAllString(line, -1) {
		u, err := url.Parse(strings.TrimRight(m, ".,;"))
		if err == nil && u.Scheme == "https" && signInHost(u.Hostname()) {
			return u.String()
		}
	}
	return ""
}

// DeviceCode returns an AWS IAM Identity Center device code (ABCD-EFGH) in a line, if any.
func DeviceCode(line string) string { return codePattern.FindString(line) }

// signIn runs a sign-in command and forwards the link (and code, when asked) it prints.
func signIn(ctx context.Context, path string, args []string, wantCode bool, p Progress) error {
	var tail []string
	err := RunTool(ctx, path, args, func(line string) {
		if l := SignInLink(line); l != "" {
			p.Prompt(l, "")
		}
		if wantCode {
			if c := DeviceCode(line); c != "" {
				p.Prompt("", c)
			}
		}
		if strings.TrimSpace(line) != "" {
			p.Line(line)
			tail = append(tail, line)
			if len(tail) > 4 {
				tail = tail[1:]
			}
		}
	})
	switch {
	case errors.Is(err, context.Canceled):
		return errors.New("sign-in cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("sign-in timed out: nothing happened in the browser for 10 minutes")
	case err != nil:
		if len(tail) > 0 {
			return fmt.Errorf("%s", truncate(strings.Join(tail, " "), 400))
		}
		return err
	}
	return nil
}
