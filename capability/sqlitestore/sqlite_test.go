package sqlitestore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/capability/internal/fixture"
	"github.com/mukezhz/codemode/capability/sqlitestore"
	"github.com/mukezhz/codemode/program"
)

// TestVectorSearch exercises the sqlite-vector backend end to end. It skips
// unless SQLITE_VECTOR_EXTENSION points at the library.
func TestVectorSearch(t *testing.T) {
	extension := os.Getenv("SQLITE_VECTOR_EXTENSION")
	if extension == "" {
		t.Skip("SQLITE_VECTOR_EXTENSION is not set")
	}
	ctx := context.Background()
	embedder := fixture.Embedder()

	path := filepath.Join(t.TempDir(), "capabilities.db")
	store, err := sqlitestore.Open(ctx, path, extension)
	if err != nil {
		t.Fatal(err)
	}
	if err := capability.Rebuild(ctx, store, fixture.Registry(), embedder); err != nil {
		t.Fatal(err)
	}
	index, err := capability.Open(ctx, store, embedder)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	matches, err := index.Search(ctx, "find unpaid bills", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Capability.Name != "invoices.listUnpaid" {
		t.Fatalf("matches = %#v", matches)
	}
}

// Blank-importing the package must make CAPABILITY_DB=sqlite selectable.
func TestBackendRegistersItself(t *testing.T) {
	t.Setenv("CAPABILITY_DB", capability.BackendSQLite)
	t.Setenv("CAPABILITY_DB_DSN", filepath.Join(t.TempDir(), "capabilities.db"))
	t.Setenv("SQLITE_VECTOR_EXTENSION", "")

	_, err := capability.OpenStoreFromEnv(context.Background())
	if err == nil {
		t.Fatal("OpenStoreFromEnv succeeded without the vector extension")
	}
	// Reaching the extension check proves the backend was selected, rather
	// than the import being missing.
	if !strings.Contains(err.Error(), "sqlite-vector extension") {
		t.Fatalf("error = %v, want the backend's own extension error", err)
	}
}

// TestProgramCache exercises the cached-program table end to end, in the same
// database file as the capability index. It skips unless
// SQLITE_VECTOR_EXTENSION points at the library.
func TestProgramCache(t *testing.T) {
	extension := os.Getenv("SQLITE_VECTOR_EXTENSION")
	if extension == "" {
		t.Skip("SQLITE_VECTOR_EXTENSION is not set")
	}
	ctx := context.Background()
	embedder := fixture.Embedder()

	path := filepath.Join(t.TempDir(), "capabilities.db")
	store, err := sqlitestore.Open(ctx, path, extension)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := capability.Rebuild(ctx, store, fixture.Registry(), embedder); err != nil {
		t.Fatal(err)
	}
	_, dimension, err := store.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}

	programStore, err := sqlitestore.OpenProgramCache(ctx, store, dimension)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := program.New(programStore, embedder, program.Options{})
	if err != nil {
		t.Fatal(err)
	}

	key := program.Key{Request: "find unpaid bills", Language: "javascript", Capabilities: "registry-1"}
	if err := cache.Put(ctx, key, "return 1;"); err != nil {
		t.Fatal(err)
	}
	entry, found, err := cache.Lookup(ctx, key)
	if err != nil || !found {
		t.Fatalf("Lookup = (found %v, err %v), want (true, nil)", found, err)
	}
	if entry.Source != "return 1;" {
		t.Fatalf("source = %q", entry.Source)
	}

	// A different scope must not see it, however close the request is.
	elsewhere := key
	elsewhere.Language = "lua"
	if _, found, err := cache.Lookup(ctx, elsewhere); err != nil || found {
		t.Fatalf("Lookup from another scope = (found %v, err %v), want (false, nil)", found, err)
	}

	if err := cache.Forget(ctx, entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := cache.Lookup(ctx, key); found {
		t.Fatal("a forgotten program was served again")
	}

	// The capability index still answers from the same database.
	index, err := capability.Open(ctx, store, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if matches, err := index.Search(ctx, "find unpaid bills", 1); err != nil || len(matches) != 1 {
		t.Fatalf("search after adding the program cache: matches %d, err %v", len(matches), err)
	}
}
