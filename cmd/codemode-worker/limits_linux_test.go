package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/mukezhz/codemode/internal/wire"
)

// The filter cannot be removed, so the probe runs in a child copy of this test binary.
func TestLimitsDenyEscapeSyscalls(t *testing.T) {
	if os.Getenv("CODEMODE_SECCOMP_PROBE") == "1" {
		if err := applyLimits(wire.Limits{TimeoutMS: 5000}); err != nil {
			t.Fatal(err)
		}
		if !seccompLoaded {
			t.Skip("seccomp unavailable on this kernel")
		}
		probes := map[string]error{}
		_, probes["open"] = os.Open("/etc/hostname")
		_, probes["dial"] = net.Dial("tcp", "127.0.0.1:1")
		probes["exec"] = exec.Command("/bin/true").Run()
		for name, err := range probes {
			if !errors.Is(err, syscall.EPERM) {
				t.Errorf("%s: err = %v, want EPERM", name, err)
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLimitsDenyEscapeSyscalls$", "-test.v")
	cmd.Env = append(os.Environ(), "CODEMODE_SECCOMP_PROBE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
