//go:build unix

package connect

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the tool in its own process group so cancelling stops the helpers it starts
// too (gcloud is a script that runs Python, which runs the local sign-in callback server).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
