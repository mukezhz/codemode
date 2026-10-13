package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	seccomp "github.com/elastic/go-seccomp-bpf"

	"github.com/mukezhz/codemode/internal/wire"
)

// denied is everything an escaped program would reach for next. A denylist, not an
// allowlist: the Go runtime's own syscalls change between releases, and a filter
// that kills the worker on a Go upgrade is worse than one that misses a syscall.
// Names must exist on both amd64 and arm64.
var denied = []string{
	"socket", "socketpair", "connect", "bind", "listen", "accept4", "sendto", "sendmsg", "recvfrom", "recvmsg",
	"execve", "execveat", "ptrace", "process_vm_readv", "process_vm_writev",
	"openat", "openat2", "open_by_handle_at", "name_to_handle_at",
	"mkdirat", "unlinkat", "renameat", "renameat2", "linkat", "symlinkat", "fchmodat", "fchownat", "truncate",
	"mount", "umount2", "pivot_root", "chroot", "unshare", "setns",
	"bpf", "perf_event_open", "keyctl", "add_key", "request_key", "io_uring_setup",
	"init_module", "finit_module", "delete_module", "kexec_load", "reboot", "swapon", "swapoff",
}

// No RLIMIT_AS: the Go runtime reserves more address space than any useful cap,
// so the worker dies before running anything. watchHeap is the memory cap, and a
// container's --memory is the hard one.
func applyLimits(limits wire.Limits) error {
	cpuSeconds := uint64(limits.TimeoutMS/1000 + 1)
	for resource, value := range map[int]uint64{
		syscall.RLIMIT_CPU:    cpuSeconds,
		syscall.RLIMIT_NOFILE: 16,
	} {
		if err := syscall.Setrlimit(resource, &syscall.Rlimit{Cur: value, Max: value}); err != nil {
			return err
		}
	}
	err := seccomp.LoadFilter(seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionAllow,
			Syscalls:      []seccomp.SyscallGroup{{Names: denied, Action: seccomp.ActionErrno}},
		},
	})
	// ponytail: fails open where the kernel or an emulator cannot load filters, like the namespace fallback; the container is the boundary.
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS) {
		fmt.Fprintf(os.Stderr, "codemode-worker: seccomp unavailable, running without it: %v\n", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("seccomp: %w", err)
	}
	seccompLoaded = true
	return nil
}

var seccompLoaded bool
