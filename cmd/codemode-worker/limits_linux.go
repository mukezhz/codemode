package main

import (
	"syscall"

	"github.com/mukezhz/codemode/internal/wire"
)

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
	return nil
}
