#!/usr/bin/env bash
# Verify what a consumer actually links.
#
# The driver split only pays off if the core packages stay free of vendor
# imports. This builds a throwaway module against the working tree and fails if
# importing codemode alone drags in anything third-party, or if the core
# packages stop building without cgo.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "==> core packages build with CGO_ENABLED=0"
CGO_ENABLED=0 go build \
	"$root/capability" "$root/capability/pgstore" "$root/codemode" \
	"$root/agent" "$root/llm" "$root/llm/claude" "$root/llm/openai" \
	"$root/sandbox" "$root/sandbox/goja" "$root/sandbox/starlark" "$root/sandbox/lua"

goversion=$(go mod edit -json "$root/go.mod" | sed -n 's/.*"Go": "\([^"]*\)".*/\1/p')
printf 'module consumer\n\ngo %s\n\nrequire github.com/mukezhz/codemode v0.0.0\n\nreplace github.com/mukezhz/codemode => %s\n' \
	"$goversion" "$root" >"$work/go.mod"
printf 'package main\n\nimport "github.com/mukezhz/codemode/codemode"\n\nfunc main() { var _ codemode.CodeMode }\n' \
	>"$work/main.go"

cd "$work"
go mod tidy >/dev/null 2>&1

echo "==> a minimal consumer links no third-party dependency"
leaked=$(go list -deps ./... |
	grep -Ev '^github\.com/mukezhz/codemode' |
	grep -E '^(github\.com|go\.starlark\.net|google\.golang\.org|golang\.org/x)' || true)
if [ -n "$leaked" ]; then
	echo "importing codemode alone pulled in:" >&2
	echo "$leaked" >&2
	exit 1
fi

CGO_ENABLED=0 go build -o /dev/null ./...
echo "==> ok"
