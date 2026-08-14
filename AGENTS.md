# Repository Guidelines

## Project Structure & Module Organization

This repository is currently empty apart from this guide. Keep the root reserved for project-level configuration and documentation. When implementation begins, place application code in `src/`, automated checks in `tests/`, and static files in `assets/`. Mirror source paths in tests where practical; for example, test `src/parser.py` with `tests/test_parser.py`. Group code by feature or domain once a flat directory becomes difficult to navigate.

## Build, Test, and Development Commands

No build system, package manager, or test runner is configured yet. Do not document or depend on placeholder commands. When adding tooling, expose a small, predictable command set through the ecosystem's standard configuration or a `Makefile`, and update this section in the same change. Prefer commands such as:

- `make test` — run the complete automated test suite.
- `make lint` — check formatting and static-analysis rules.
- `make run` — start the project locally.

Each command should work from the repository root and fail with a nonzero exit code on errors.

## Coding Style & Naming Conventions

Use the language's standard formatter and linter rather than hand-maintained style rules. Keep indentation consistent with the formatter. Use descriptive names: `snake_case` for Python files and functions, `camelCase` for JavaScript or TypeScript variables, and `PascalCase` for classes and components. Avoid speculative abstractions; prefer the smallest clear implementation and reuse existing helpers before adding new ones.

## Testing Guidelines

Add a focused test for every non-trivial branch, parser, bug fix, and security-sensitive path. Keep tests deterministic and independent of network services unless explicitly marked as integration tests. Name tests according to the chosen framework and keep fixtures small. Document coverage thresholds only after coverage tooling is configured.

## Commit & Pull Request Guidelines

There is no Git history from which to infer a commit convention. Until one is established, use concise imperative subjects, for example `Add input validation`. Keep commits focused. Pull requests should explain the problem and solution, list verification performed, link relevant issues, and include screenshots for visible UI changes. Never commit secrets; provide redacted examples such as `.env.example` for required configuration.
