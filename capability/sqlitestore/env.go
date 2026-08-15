// This file is the only place in the package that reads the process
// environment. Open takes typed arguments and consults nothing else.

package sqlitestore

import (
	"context"
	"os"
	"strings"

	"github.com/mukezhz/code-mode/capability"
)

// EnvExtension names the sqlite-vector library. There is no default: loading an
// arbitrary extension found by search would be a code-execution hole, so the
// path is always explicit.
const EnvExtension = "SQLITE_VECTOR_EXTENSION"

func init() {
	capability.RegisterBackend(capability.Backend{
		Name:        capability.BackendSQLite,
		Aliases:     []string{"sqlite3"},
		DSN:         "the SQLite database path",
		OpenFromEnv: OpenFromEnv,
	})
}

// OpenFromEnv opens the SQLite index at path, taking the vector library from
// SQLITE_VECTOR_EXTENSION. Call Open instead to pass the path yourself.
func OpenFromEnv(ctx context.Context, path string) (capability.Store, error) {
	return Open(ctx, path, strings.TrimSpace(os.Getenv(EnvExtension)))
}
