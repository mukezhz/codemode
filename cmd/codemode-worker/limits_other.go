//go:build !linux

package main

import "github.com/mukezhz/codemode/internal/wire"

// Elsewhere the heap watchdog and the host's timeout are the limits.
func applyLimits(wire.Limits) error { return nil }
