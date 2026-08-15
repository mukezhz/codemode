# 0001 — Split published docs from internal notes

**Status:** accepted
**Date:** 2026-08-15

## Context

Documentation started as two files at `docs/`: a 520-line `README.md` and a
hand-authored `flow.html`. Both had drifted from the code — the README still
described the sandbox and model integration as unimplemented after both shipped,
and the HTML could not be split, linked between, or contributed to.

Converting to a generated site raised two structural questions at once:

1. Where does the Node toolchain live in an otherwise pure-Go repository?
2. Where do PRDs, milestones, and decision records go once they exist?

These look separate but share an answer, because both are really asking what the
`docs/` directory means.

## Decision

`docs/` is the published site and nothing else. It holds its own
`package.json`, lockfile, and `node_modules`, its VitePress config, and its
pages under `content/`. Deleting the directory removes every trace of Node from
the repository.

`notes/` is internal writing — PRDs, milestones, and ADRs. No tooling, no build
step, never published.

Node and pnpm are pinned in `mise.toml` beside Buf, so a fresh checkout gets a
working toolchain from `mise install` without ambient versions.

## Alternatives considered

**Everything in `docs/`, internal pages excluded by config.** One place to look,
but publication becomes a rule someone has to remember rather than a property of
where the file sits. A forgotten `srcExclude` entry publishes a PRD.

**`package.json` at the repository root.** Conventional, and what the first
version did. Rejected because it splits one concern — building the site — across
two directories, and puts JavaScript tooling in the first thing a Go developer
sees.

**Markdown at `docs/*.md` with the toolchain alongside it.** Mixes
`package.json`, `node_modules`, and prose in one listing. The `content/`
subdirectory keeps the writing surface obvious to someone who does not care
about the build.

**Hugo instead of VitePress.** A single Go binary with no Node at all, which
fits the repository's language. Rejected because VitePress gives search, an
authored sidebar, and mermaid with less configuration, and the toolchain cost is
contained by the directory boundary above. Worth revisiting if the Node
dependency becomes a burden.

## Consequences

Diagrams move from hand-authored SVG to `mermaid` fences. Some precision is lost
— mermaid chooses its own layout — in exchange for diagrams a contributor will
actually edit when they add an engine or a backend.

Markdown is also the input format Hugo and Docusaurus take, so switching
generators later is a configuration change rather than a content rewrite.

`make docs` and `make docs-build` are the entry points; both delegate to pnpm
with `--dir docs`.
