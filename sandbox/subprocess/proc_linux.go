package subprocess

import (
	"errors"
	"os/exec"
	"syscall"
)

const canUnshare = true

// isolate also unshares network and mounts when allowed, and kills the worker if the host dies.
func isolate(cmd *exec.Cmd, namespaces bool) {
	attrs := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if namespaces {
		attrs.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET | syscall.CLONE_NEWNS
	}
	cmd.SysProcAttr = attrs
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

func denied(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSPC)
}
