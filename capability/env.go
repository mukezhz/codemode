package capability

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	EnvBackend = "CAPABILITY_DB"
	EnvDSN     = "CAPABILITY_DB_DSN"
)

// Returns ErrNoStore when nothing is configured; the caller closes the store.
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
