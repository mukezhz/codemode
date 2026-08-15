package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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

// Backend describes one registered store implementation.
type Backend struct {
	// Name is the canonical name that selects it, such as "postgres".
	Name string
	// Aliases are other accepted spellings, such as "postgresql".
	Aliases []string
	// OpenFromEnv connects to dsn using this backend's own environment for
	// anything else it needs. Only OpenStoreFromEnv calls it; a backend is
	// otherwise opened by calling its package's Open directly, which takes
	// typed arguments and reads nothing.
	OpenFromEnv func(ctx context.Context, dsn string) (Store, error)
	// DSN describes what the DSN should hold, for the error raised when it is
	// empty.
	DSN string
}

// Backends this module ships, and the package that registers each. Blank-import
// the one you want; a binary then links only that driver, which is what keeps
// cgo out of a build that only uses Postgres.
//
// Registration exists for OpenStoreFromEnv. To open a store from your own
// configuration, call the backend package's Open directly — it takes typed
// arguments and reads no environment.
const (
	// BackendSQLite indexes in SQLite with the sqlite-vector extension.
	// Package capability/sqlitestore. Requires cgo.
	BackendSQLite = "sqlite"
	// BackendPostgres indexes in PostgreSQL with pgvector.
	// Package capability/pgstore. Pure Go.
	BackendPostgres = "postgres"
)

// DefaultBackend is used when CAPABILITY_DB is unset but a DSN is given.
const DefaultBackend = BackendSQLite

var shipped = map[string]string{
	BackendSQLite:   "github.com/mukezhz/code-mode/capability/sqlitestore",
	BackendPostgres: "github.com/mukezhz/code-mode/capability/pgstore",
}

var (
	backendsMutex sync.RWMutex
	backends      = map[string]Backend{}
)

// RegisterBackend makes backend selectable by OpenStoreFromEnv. Store packages call it
// from init, so blank-importing one is enough. Registering a name twice panics,
// which catches a duplicate import path at startup rather than in production.
func RegisterBackend(backend Backend) {
	if backend.Name == "" || backend.OpenFromEnv == nil {
		panic("capability: backend needs a name and an opener")
	}
	backendsMutex.Lock()
	defer backendsMutex.Unlock()
	for _, name := range append([]string{backend.Name}, backend.Aliases...) {
		if _, exists := backends[name]; exists {
			panic("capability: backend " + name + " is already registered")
		}
		backends[name] = backend
	}
}

// RegisteredBackends lists the canonical names of every registered backend,
// sorted.
func RegisteredBackends() []string {
	backendsMutex.RLock()
	defer backendsMutex.RUnlock()
	unique := map[string]bool{}
	for _, backend := range backends {
		unique[backend.Name] = true
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// unregisteredBackend distinguishes "you forgot the import" from "no such
// backend", because the fix is different and the first is far more common.
func unregisteredBackend(name string) error {
	if path, ships := shipped[name]; ships {
		return fmt.Errorf("capability store %q is not registered: add `_ %q` to your imports", name, path)
	}
	available := RegisteredBackends()
	if len(available) == 0 {
		return fmt.Errorf("capability store %q is unknown and none are registered: blank-import %s or %s",
			name, shipped[BackendSQLite], shipped[BackendPostgres])
	}
	return fmt.Errorf("capability store %q is unknown: registered backends are %s",
		name, strings.Join(available, ", "))
}

// RowScanner is the part of *sql.Rows a backend query returns. It is an
// interface so ScanMatches works for any driver.
type RowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

// ScanMatches decodes capability rows in the column order every backend query
// selects: name, rpc, description, input, output, distance. Backends outside
// this module use it to stay consistent with the ones inside it.
func ScanMatches(rows RowScanner, limit int) ([]Match, error) {
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
		if err := DecodeSchemas(&match.Capability, input, output); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, rows.Err()
}

// EncodeSchemas renders a capability's input and output schemas for storage.
func EncodeSchemas(item Capability) (input, output []byte, err error) {
	if input, err = json.Marshal(item.Input); err != nil {
		return nil, nil, err
	}
	if output, err = json.Marshal(item.Output); err != nil {
		return nil, nil, err
	}
	return input, output, nil
}

// DecodeSchemas is the inverse of EncodeSchemas.
func DecodeSchemas(item *Capability, input, output []byte) error {
	if err := json.Unmarshal(input, &item.Input); err != nil {
		return err
	}
	return json.Unmarshal(output, &item.Output)
}

// lookupBackend returns the backend registered under name.
func lookupBackend(name string) (Backend, bool) {
	backendsMutex.RLock()
	defer backendsMutex.RUnlock()
	backend, found := backends[name]
	return backend, found
}
