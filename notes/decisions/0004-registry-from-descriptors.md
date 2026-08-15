# 0004 — Derive the registry from linked descriptors

**Status:** accepted
**Date:** 2026-08-15

## Context

Getting a registry required four steps in a consuming project: copy
`code_mode.proto` into your buf module, run `buf build --as-file-descriptor-set`,
run `codemode-registry`, then `go:embed` the JSON. Three of those are build
plumbing, and the artifact can silently fall out of step with the protos — a
stale `registry.json` offers a model capabilities the host will reject.

It also forced buf on every consumer, including ones already generating Go with
`protoc` directly.

## Decision

`registry.FromFiles(...protoreflect.FileDescriptor)` builds the registry from
descriptors already compiled into the binary:

```go
capabilities, err := registry.FromFiles(todov1.File_todo_proto)
```

`protoc-gen-go` embeds the file descriptor in the package it generates. A
consumer already imports that package to implement the service, so nothing new
is linked and nothing is generated. Imports are followed transitively, which
picks up both the request and response messages and `code_mode.proto` itself.

`registry.FromGlobalFiles()` scans `protoregistry.GlobalFiles` instead. Only
services carrying `(code_mode.namespace)` contribute, so an unrelated dependency
cannot widen the registry — but naming the files stays the documented habit,
because it states the exposed surface in code.

`codemode-registry` and `registry.json` remain supported. A checked-in registry
makes a change to the capability surface appear as a diff in a pull request, and
for a surface a model calls, that is worth a build step. The choice is now the
project's rather than the library's.

## The bug this surfaced

`stringOption` read the custom options out of the message's *unknown* fields,
which is correct only when `code_mode.proto`'s extensions are not linked. On the
`FromFiles` path the consumer's generated `code_mode` package registers them, so
the options resolve and the unknown fields are empty — every namespace came back
blank, and the error blamed the regex.

Both cases now work: resolved extensions are read with `proto.RangeExtensions`,
and unknown fields remain the fallback. A test builds the registry both ways and
asserts the JSON is byte-identical, so the paths cannot drift.

## Consequences

`gen/` is committed. A consumer running `go get` has no buf, and the example
needs the descriptors to demonstrate `FromFiles`. CI regenerates and diffs it.

`buf.gen.yaml` uses the `go_package` already declared in each proto rather than
managed mode, because the protos live flat in `proto/` with different packages
and `paths=source_relative` collapsed them into one directory.
