# 0003 — The library takes configuration, not environment

**Status:** accepted
**Date:** 2026-08-15

## Context

Every constructor read `os.Getenv`: `llm.New` read `LLM_PROVIDER` and
`ANTHROPIC_MODEL`, `sandbox.New` read `SANDBOX_ENGINE`, `capability.OpenStore`
read `CAPABILITY_DB`. That is fine for a command; it is wrong for a library.

A consuming application already has a way to configure itself — flags, a config
file, viper or koanf, a mounted secret, a Kubernetes ConfigMap. A library that
reaches into the environment claims a global namespace it does not own, competes
with that mechanism, and makes its own behaviour depend on process state the
caller cannot see at the call site. It also makes tests order-dependent, since
`t.Setenv` is process-global.

## Decision

Every constructor takes explicit options and reads nothing else. The environment
is an opt-in layer, and the opt-in is visible in the name:

| Explicit | From the environment |
| --- | --- |
| `llm.New(Options)` | `llm.OptionsFromEnv()`, `llm.NewFromEnv()` |
| `claude.New(Options)` | `claude.OptionsFromEnv()`, `claude.NewFromEnv()` |
| `sandbox.New(Options)` | `sandbox.OptionsFromEnv()`, `sandbox.NewFromEnv()` |
| `pgstore.Open(ctx, dsn)` | `capability.OpenStoreFromEnv(ctx)` |

`OptionsFromEnv` returns a value rather than a client, so an application can log
it, override any field, and pass the result to `New`. `Options.Merge` layers the
caller's configuration over the environment's, receiver wins.

`agent.Options.Runner` became required for the same reason: it used to build a
runner from `SANDBOX_ENGINE` when nil, which meant the engine untrusted programs
ran in was decided by process state nobody had written down.

**`scripts/check-no-env.sh` enforces this.** `os.Getenv` outside a file named
`env.go` fails the build. It runs in `make test` and CI, so the rule is
structural rather than a convention that decays.

## Alternatives considered

**Functional options (`llm.New(llm.WithModel("x"))`).** Idiomatic, and it makes
"unset" unambiguous without pointers. Rejected because a config struct is
directly unmarshalable — an application can decode YAML or JSON straight into
`llm.Options` — and because the provider-specific structs embed `llm.Options`,
which a functional-option API cannot express as cleanly. `*float64` plus
`llm.Float` covers the one field where zero is meaningful.

**Keep env reading, document it.** Cheapest. Rejected: the surprise is the
problem, and documentation does not remove it.

**Drop environment support entirely.** Purest, and wrong for the demo and for
the many applications that genuinely are twelve-factor. The `FromEnv` layer
costs one file per package and makes the choice explicit at the call site.

## Consequences

Breaking: `llm.New()` → `llm.New(Options)`, `capability.OpenStore` →
`OpenStoreFromEnv`, `agent.Options.Runner` is required, `Provider.Configured`
became `Provider.FromEnv`. All pre-tag.

`llm.Defaults` shrank to `{Model, MaxTokens}` — it used to carry the names of
environment variables, which no longer belong in the pure path.

Each package's environment variable names are now exported constants
(`llm.EnvProvider`, `sandbox.EnvEngine`, `sqlitestore.EnvExtension`), so an
application that wants to document or validate them does not hardcode strings.
