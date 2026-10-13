# 0005 — Out-of-process sandbox over JSON lines

**Status:** accepted
**Date:** 2026-10-13

## Context

The goja, Starlark and Lua engines share the host's heap. A program that
allocates in a loop exhausts host memory long before its timeout, and an
interpreter escape lands in a process that holds every credential. Both get worse
once end users write the prompts that steer the program.

## Decision

Add `sandbox/subprocess`, which runs each program in a fresh `codemode-worker`
child and speaks JSON lines over stdio (`internal/wire`).

- **The host enforces every limit that matters**: call budget, per-call response
  size, line size, wall clock, empty environment, process-group kill. The worker
  is untrusted once program code runs in it, so nothing it reports is believed.
- **The worker enforces only its own resources**: a heap watchdog (portable;
  `RLIMIT_AS` cannot cap a Go process) and, on Linux, CPU and fd rlimits. These stop runaway programs,
  not an attacker who already escaped.
- **The engines are reused unchanged.** The worker hands them a `CodeMode` whose
  `Execute` is a round trip, so there is still exactly one implementation of each
  language.
- **One process per run, no pool.** Reusing a worker turns one escape into
  cross-request leakage. A pool waits until spawn latency is measured.

## Alternatives

- **gRPC between host and worker**: codegen and a listener for a strictly
  sequential request/response stream; JSON lines are debuggable with `echo`.
- **wasm**: memory-isolated in-process, but no mature goja/Starlark/Lua builds,
  and it changes every engine rather than adding one package.
- **Container first**: needed for multi-tenant, but the container runs this same
  worker, so this is the step it builds on.
