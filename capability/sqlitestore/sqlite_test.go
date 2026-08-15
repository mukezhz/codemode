package sqlitestore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/capability/internal/fixture"
	"github.com/mukezhz/code-mode/capability/sqlitestore"
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
