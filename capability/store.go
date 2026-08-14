package capability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Record is one capability contract paired with its embedding.
type Record struct {
	Capability Capability
	Vector     []float32
}

// Store holds embedded capability contracts and answers nearest-neighbour
// queries over them. Implementations are backend-specific; the ranking policy
// (cosine distance, closest first) is the same for all of them.
type Store interface {
	// Rebuild replaces the index contents with records, recording the
	// embedding model and vector dimension they were produced with.
	Rebuild(ctx context.Context, model string, dimension int, records []Record) error

	// Metadata returns the embedding model and dimension the index was built
	// with, so a caller can reject a mismatched embedder before querying.
	Metadata(ctx context.Context) (model string, dimension int, err error)

	// Search returns at most limit capabilities nearest to vector, ordered
	// from closest to least close.
	Search(ctx context.Context, vector []float32, limit int) ([]Match, error)

	// Close releases the backend connection.
	Close() error
}

// ErrNoStore reports that no capability store is configured.
var ErrNoStore = errors.New("no capability store configured")

// OpenStore builds the store named by CAPABILITY_DB: "sqlite" (the default) or
// "postgres". Ctx carries cancellation.
//
// Environment:
//
//	CAPABILITY_DB            sqlite | postgres      (default sqlite)
//	CAPABILITY_DB_DSN        SQLite file path, or a Postgres connection URL
//	SQLITE_VECTOR_EXTENSION  absolute path to the sqlite-vector library
//
// The caller closes the returned store.
func OpenStore(ctx context.Context) (Store, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("CAPABILITY_DB")))
	dsn := strings.TrimSpace(os.Getenv("CAPABILITY_DB_DSN"))

	switch backend {
	case "sqlite", "sqlite3", "":
		if dsn == "" {
			if backend == "" {
				return nil, fmt.Errorf("%w: set CAPABILITY_DB_DSN", ErrNoStore)
			}
			return nil, errors.New("CAPABILITY_DB_DSN must be the SQLite database path")
		}
		return OpenSQLite(ctx, dsn, strings.TrimSpace(os.Getenv("SQLITE_VECTOR_EXTENSION")))
	case "postgres", "postgresql", "pgx":
		if dsn == "" {
			return nil, errors.New("CAPABILITY_DB_DSN must be the Postgres connection URL")
		}
		return OpenPostgres(ctx, dsn)
	default:
		return nil, fmt.Errorf("CAPABILITY_DB %q is not supported: use sqlite or postgres", backend)
	}
}

// scanMatches decodes capability rows in the column order every backend query
// selects: name, rpc, description, input, output, distance.
func scanMatches(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}, limit int) ([]Match, error) {
	matches := make([]Match, 0, limit)
	for rows.Next() {
		var match Match
		var input, output []byte
		if err := rows.Scan(
			&match.Capability.Name,
			&match.Capability.RPC,
			&match.Capability.Description,
			&input,
			&output,
			&match.Distance,
		); err != nil {
			return nil, err
		}
		if err := decodeSchemas(&match.Capability, input, output); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, rows.Err()
}
