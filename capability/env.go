package capability

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Environment variables read by OpenStoreFromEnv.
const (
	// EnvBackend selects the backend: sqlite or postgres.
	EnvBackend = "CAPABILITY_DB"
	// EnvDSN is the SQLite file path or the Postgres connection URL.
	EnvDSN = "CAPABILITY_DB_DSN"
)

// OpenStoreFromEnv opens the registered backend named by CAPABILITY_DB using CAPABILITY_DB_DSN.
// It returns ErrNoStore when nothing is configured; the caller closes the store.
func OpenStoreFromEnv(ctx context.Context) (Store, error) {
	name := strings.ToLower(strings.TrimSpace(os.Getenv(EnvBackend)))
	dsn := strings.TrimSpace(os.Getenv(EnvDSN))
	if name == "" {
		if dsn == "" {
			return nil, fmt.Errorf("%w: set %s", ErrNoStore, EnvDSN)
		}
		name = DefaultBackend
	}

	backend, found := lookupBackend(name)
	if !found {
		return nil, unregisteredBackend(name)
	}
	if dsn == "" {
		return nil, fmt.Errorf("%s must be %s", EnvDSN, backend.DSN)
	}
	return backend.OpenFromEnv(ctx, dsn)
}
