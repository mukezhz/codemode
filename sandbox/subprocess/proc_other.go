//go:build !unix

package subprocess

import "os/exec"

const canUnshare = false

func isolate(*exec.Cmd, bool) {}

func denied(error) bool { return false }
