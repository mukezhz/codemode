// This file is the only place in the package that reads the process
// environment. Everything else takes typed arguments.

package capability

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Environment variables read by OpenStoreFromEnv. Backend-specific variables
// live with their backend package.
const (
	// EnvBackend selects the backend: sqlite or postgres.
	EnvBackend = "CAPABILITY_DB"
	// EnvDSN is the SQLite file path or the Postgres connection URL.
	EnvDSN = "CAPABILITY_DB_DSN"
)

// OpenStoreFromEnv builds the store named by CAPABILITY_DB from the registered
// backends, using CAPABILITY_DB_DSN and whatever else that backend reads.
//
// This is the convenience path. To open a store from your own configuration,
// call the backend package directly — pgstore.Open(ctx, dsn) or
// sqlitestore.Open(ctx, path, extensionPath) — which takes typed arguments and
// reads no environment.
//
// It returns ErrNoStore when nothing is configured at all, which a caller can
// treat as "semantic search is off" rather than as a failure. The caller closes
// the returned store.
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
