//go:build unix && !linux

package subprocess

import (
	"os/exec"
	"syscall"
)

const canUnshare = false

// isolate gives the worker its own process group, so a timeout kills anything it spawned.
func isolate(cmd *exec.Cmd, _ bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

func denied(error) bool { return false }
