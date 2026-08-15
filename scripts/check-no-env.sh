#!/usr/bin/env bash
# Enforce the library's configuration rule.
#
# A library that reaches into os.Getenv claims a namespace it does not own and
# fights the host application's own configuration. So every constructor here
# takes explicit options, and only a file named env.go may read the environment
# — where a name containing FromEnv makes that opt-in visible at the call site.
#
# This is a structural check, not a convention: if os.Getenv appears anywhere
# else in the library, the build fails.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

# examples/ is an application, not the library. It is expected to read the
# environment, and demonstrates the FromEnv path.
# Line-numbered so comment lines can be dropped: the packages document the rule
# by naming os.Getenv in prose, and prose is not a violation.
offenders=$(grep -rn --include='*.go' -e 'os\.Getenv' -e 'os\.LookupEnv' \
	capability codemode llm sandbox agent registry proto cmd 2>/dev/null |
	grep -v '_test\.go:' |
	grep -v '/env\.go:' |
	grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' || true)

if [ -n "$offenders" ]; then
	echo "these library files read the environment outside env.go:" >&2
	echo "$offenders" >&2
	echo >&2
	echo "move the reads into the package's env.go behind a FromEnv name," >&2
	echo "and take the value as an option everywhere else." >&2
	exit 1
fi

echo "==> no library package reads the environment outside env.go"
