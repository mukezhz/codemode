package pgstore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/capability/internal/fixture"
	"github.com/mukezhz/codemode/capability/pgstore"
)

// TestPostgresSearch exercises the pgvector backend end to end. It skips
// unless CAPABILITY_TEST_POSTGRES_DSN points at a database with pgvector.
func TestPostgresSearch(t *testing.T) {
	dsn := os.Getenv("CAPABILITY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CAPABILITY_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()

	store, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	embedder := fixture.Embedder()
	if err := capability.Rebuild(ctx, store, fixture.Registry(), embedder); err != nil {
		t.Fatal(err)
	}
	index, err := capability.Open(ctx, store, embedder)
	if err != nil {
		t.Fatal(err)
	}

	matches, err := index.Search(ctx, "find unpaid bills", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	if matches[0].Capability.Name != "invoices.listUnpaid" {
		t.Fatalf("closest = %q, want invoices.listUnpaid", matches[0].Capability.Name)
	}
	if matches[0].Distance > matches[1].Distance {
		t.Fatalf("matches are not ordered closest first: %v", matches)
	}
	// Schemas must survive the JSON round trip through the database.
	if matches[0].Capability.RPC != "invoice.v1.InvoiceService/ListUnpaid" {
		t.Fatalf("rpc = %q", matches[0].Capability.RPC)
	}
}

// Blank-importing the package must make CAPABILITY_DB=postgres selectable.
func TestBackendRegistersItself(t *testing.T) {
	t.Setenv("CAPABILITY_DB", capability.BackendPostgres)
	t.Setenv("CAPABILITY_DB_DSN", "")
	_, err := capability.OpenStoreFromEnv(context.Background())
	if err == nil {
		t.Fatal("OpenStoreFromEnv succeeded without a DSN")
	}
	if !strings.Contains(err.Error(), "the Postgres connection URL") {
		t.Fatalf("error = %v, want the backend's own DSN description", err)
	}
}
