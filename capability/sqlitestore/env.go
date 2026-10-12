package sqlitestore

import (
	"context"
	"os"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

// EnvExtension names the sqlite-vector library; there is no default, since searching for one would allow loading arbitrary code.
const EnvExtension = "SQLITE_VECTOR_EXTENSION"

func init() {
	capability.RegisterBackend(capability.Backend{
		Name:        capability.BackendSQLite,
		Aliases:     []string{"sqlite3"},
		DSN:         "the SQLite database path",
		OpenFromEnv: OpenFromEnv,
	})
}

func OpenFromEnv(ctx context.Context, path string) (capability.Store, error) {
	return Open(ctx, path, strings.TrimSpace(os.Getenv(EnvExtension)))
}
