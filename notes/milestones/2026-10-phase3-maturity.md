# Phase 3 — Maturity

Parent: [2026-10-hardening.md](2026-10-hardening.md) · Can run alongside phase 2

## Problem

Stars do not make the sandbox safer. What a reviewer (or future you) needs is
evidence: a written threat model, tests that try to break it, automated checks
that catch regressions from dependency bumps, and versioned releases to pin.

## Goal

Each item below produces an artifact someone can check. Nothing here is a badge.

## Already done (CI)

gofmt, `go vet`, `go test -race ./...`, pgvector test, consumer check,
`check-no-env.sh`, buf lint, generated-code diff, docs build. Do not duplicate.

## Milestones

### M3.1 Threat model — `SECURITY.md`

Sections:
1. **What codemode trusts.** The host process, its `ExecuteFunc`, the protos.
2. **What it does not trust.** The program (model-written, steerable by prompt
   injection through user input or tool output).
3. **Guarantees per runner.**

   | Runner | Stops | Does not stop |
   | --- | --- | --- |
   | goja/starlark/lua in-process | ambient access (no fs, net, env), unregistered capabilities, call/time/response budgets | memory exhaustion of host, interpreter escape |
   | subprocess-* (macOS) | above + host memory exhaustion, env leakage | escape → user-level access to host fs/net |
   | subprocess-* (Linux) | above + no network, private mount ns | kernel exploits |
   | container + gVisor (phase 4) | above + kernel attack surface | — (supported boundary for multi-tenant) |

4. **What is always the host's job.** Row-level scoping in `ExecuteFunc`, roles in
   protos, read-only registries for reporting.
5. **Reporting.** Address, expected response time, no public issues for vulns.

Also: replace the README warning with a one-line link to `SECURITY.md`.

### M3.2 Fuzzing

- `internal/wire`: `FuzzDecode` (from phase 1).
- `registry`: `FuzzFromFiles` over arbitrary `FileDescriptorSet` bytes; property:
  never panics, either error or a registry where every capability has a name.
- `capability`: `FuzzTypeSchema` if schema conversion has a parser path.
- CI job `fuzz`: `go test -run=^$ -fuzz=. -fuzztime=30s` per target, on PRs only
  touching those packages (path filter), nightly for all.
- Crashers committed to `testdata/fuzz/` become regression tests automatically.

### M3.3 Dependency hygiene

- CI step `govulncheck ./...` (fails on reachable vulns only).
- Dependabot (or Renovate) for `gomod` and `github-actions`, weekly, grouped.
- Engine bumps (goja, starlark, gopher-lua) get their own PRs, never grouped, so
  the escape suite in `sandbox/enginetest` is what approves them.
- Actions stay on major version tags (`@v5`); Dependabot keeps them current.

### M3.4 Releases

- `CHANGELOG.md` in Keep a Changelog format, starting at `v0.1.0`.
- Tag `v0.1.0` after phase 1 merges. Semver: while `v0`, minor = breaking.
- GitHub release builds `codemode-worker` for linux/darwin × amd64/arm64 with
  `CGO_ENABLED=0` (GoReleaser or a 20-line workflow; prefer the workflow) and
  publishes checksums.
- Commit convention: Conventional Commits (history already uses `feat:`/`fix:`/`chore:`);
  record in `AGENTS.md`, which currently says no convention exists.

### M3.5 Observability (minimal)

No OpenTelemetry dependency yet. Instead:
- Doc snippet in `docs/content/configuration.md` showing `agent.Options.OnCall`
  feeding `log/slog` and, separately, an OTel span — copy-paste for hosts that
  already run OTel.
- Revisit a real `otel` adapter package when a production host asks.

### M3.6 Dogfooding loop

- Every production bug in a host product becomes a test here before the fix.
  Add a line to `AGENTS.md` testing guidelines.

Done when: `SECURITY.md` merged, fuzz + govulncheck running in CI, `v0.1.0`
tagged with worker binaries attached.

## Out of scope

Coverage thresholds, OpenSSF badges, a CLA, a website redesign.
