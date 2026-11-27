//go:build !unix

package connect

import "os/exec"

func setProcessGroup(*exec.Cmd) {}
