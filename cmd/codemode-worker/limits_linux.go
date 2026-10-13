package main

import (
	"syscall"

	"github.com/mukezhz/codemode/internal/wire"
)

func applyLimits(limits wire.Limits) error {
	// ponytail: the Go runtime reserves address space up front, so RLIMIT_AS is a loose backstop; watchHeap is the real cap.
	addressSpace := uint64(max(limits.MaxHeapBytes*4, 1<<30))
	cpuSeconds := uint64(limits.TimeoutMS/1000 + 1)
	for resource, value := range map[int]uint64{
		syscall.RLIMIT_AS:     addressSpace,
		syscall.RLIMIT_CPU:    cpuSeconds,
		syscall.RLIMIT_NOFILE: 16,
	} {
		if err := syscall.Setrlimit(resource, &syscall.Rlimit{Cur: value, Max: value}); err != nil {
			return err
		}
	}
	return nil
}
