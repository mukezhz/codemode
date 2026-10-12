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

// Store holds embedded capability contracts and answers nearest-neighbour queries by cosine distance.
type Store interface {
	// Rebuild replaces the index contents with records and the model and dimension that produced them.
	Rebuild(ctx context.Context, model string, dimension int, records []Record) error

	// Metadata returns the embedding model and dimension the index was built with.
	Metadata(ctx context.Context) (model string, dimension int, err error)

	// Search returns at most limit capabilities nearest to vector, closest first.
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
	// OpenFromEnv connects to dsn, reading any other settings from the environment.
	OpenFromEnv func(ctx context.Context, dsn string) (Store, error)
	// DSN describes the expected DSN, for the error raised when it is empty.
	DSN string
}

// Shipped backend names; blank-import the matching package to register one.
const (
	// BackendSQLite is capability/sqlitestore (sqlite-vector); requires cgo.
	BackendSQLite = "sqlite"
	// BackendPostgres is capability/pgstore (pgvector); pure Go.
	BackendPostgres = "postgres"
)

// DefaultBackend is used when CAPABILITY_DB is unset but a DSN is given.
const DefaultBackend = BackendSQLite

var shipped = map[string]string{
	BackendSQLite:   "github.com/mukezhz/codemode/capability/sqlitestore",
	BackendPostgres: "github.com/mukezhz/codemode/capability/pgstore",
}

var (
	backendsMutex sync.RWMutex
	backends      = map[string]Backend{}
)

// RegisterBackend makes backend selectable by OpenStoreFromEnv; registering a name twice panics.
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

// RegisteredBackends lists the canonical names of every registered backend, sorted.
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

// unregisteredBackend distinguishes a missing import from an unknown backend.
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

// RowScanner is the part of *sql.Rows that ScanMatches needs.
type RowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

// ScanMatches decodes rows in the column order name, rpc, description, input, output, distance.
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

func lookupBackend(name string) (Backend, bool) {
	backendsMutex.RLock()
	defer backendsMutex.RUnlock()
	backend, found := backends[name]
	return backend, found
}
