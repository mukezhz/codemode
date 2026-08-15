package capability_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/capability/internal/fixture"
)

// A backend registered only by this test, so registration mechanics are
// exercised without linking a real driver.
func init() {
	capability.RegisterBackend(capability.Backend{
		Name:    "stub",
		Aliases: []string{"stubby"},
		DSN:     "anything at all",
		Open: func(context.Context, string) (capability.Store, error) {
			return &fixture.Memory{}, nil
		},
	})
}

func TestIndexRanksBySimilarity(t *testing.T) {
	ctx := context.Background()
	store := &fixture.Memory{}
	embedder := fixture.Embedder()

	if err := capability.Rebuild(ctx, store, fixture.Registry(), embedder); err != nil {
		t.Fatalf("Rebuild returned error: %v", err)
	}

	index, err := capability.Open(ctx, store, embedder)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	matches, err := index.Search(ctx, "find unpaid bills", 1)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(matches) != 1 || matches[0].Capability.Name != "invoices.listUnpaid" {
		t.Fatalf("matches = %#v", matches)
	}

	if err := index.Close(); err != nil || !store.Closed {
		t.Fatalf("Close did not reach the store (err %v, closed %v)", err, store.Closed)
	}
}

func TestOpenRejectsAMismatchedEmbeddingModel(t *testing.T) {
	ctx := context.Background()
	store := &fixture.Memory{}
	if err := capability.Rebuild(ctx, store, fixture.Registry(), fixture.Embedder()); err != nil {
		t.Fatal(err)
	}

	other := fixture.Embedder()
	other.Model = "different-embedding-v2"
	_, err := capability.Open(ctx, store, other)
	if err == nil {
		t.Fatal("Open returned no error for a mismatched embedding model")
	}
	if !strings.Contains(err.Error(), "different-embedding-v2") {
		t.Fatalf("error = %v, want it to name the requested model", err)
	}
}

func TestRebuildRejectsRaggedVectors(t *testing.T) {
	first := true
	embedder := capability.Embedder{Model: "ragged", Embed: func(context.Context, string) ([]float32, error) {
		if first {
			first = false
			return []float32{1, 0, 0}, nil
		}
		return []float32{1, 0}, nil
	}}

	err := capability.Rebuild(context.Background(), &fixture.Memory{}, fixture.Registry(), embedder)
	if err == nil {
		t.Fatal("Rebuild returned no error for vectors of differing dimension")
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error = %v, want a dimension mismatch", err)
	}
}

func TestOpenStoreSelectsARegisteredBackend(t *testing.T) {
	for _, name := range []string{"stub", "stubby"} {
		t.Setenv("CAPABILITY_DB", name)
		t.Setenv("CAPABILITY_DB_DSN", "anything")
		store, err := capability.OpenStore(context.Background())
		if err != nil {
			t.Fatalf("OpenStore(%q) returned error: %v", name, err)
		}
		store.Close()
	}
}

// A missing blank import is the common failure of this design, so the error
// has to name the package rather than say "unsupported".
func TestOpenStoreNamesTheMissingImportForAShippedBackend(t *testing.T) {
	for backend, want := range map[string]string{
		capability.BackendSQLite:   "capability/sqlitestore",
		capability.BackendPostgres: "capability/pgstore",
	} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("CAPABILITY_DB", backend)
			t.Setenv("CAPABILITY_DB_DSN", "anything")
			_, err := capability.OpenStore(context.Background())
			if err == nil {
				t.Fatal("OpenStore unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to name %q", err, want)
			}
		})
	}
}

func TestOpenStoreReportsMissingConfiguration(t *testing.T) {
	for name, test := range map[string]struct {
		env  map[string]string
		want string
	}{
		"no dsn for a registered backend": {
			map[string]string{"CAPABILITY_DB": "stub"},
			"CAPABILITY_DB_DSN must be anything at all",
		},
		"unknown backend": {
			map[string]string{"CAPABILITY_DB": "mysql", "CAPABILITY_DB_DSN": "x"},
			`"mysql" is unknown`,
		},
		"nothing configured": {
			map[string]string{},
			"set CAPABILITY_DB_DSN",
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, key := range []string{"CAPABILITY_DB", "CAPABILITY_DB_DSN"} {
				t.Setenv(key, test.env[key])
			}
			store, err := capability.OpenStore(context.Background())
			if err == nil {
				store.Close()
				t.Fatal("OpenStore unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

// ErrNoStore is the signal a caller treats as "semantic search is off", so it
// must survive the wrapping OpenStore does.
func TestOpenStoreReportsErrNoStoreWhenUnconfigured(t *testing.T) {
	t.Setenv("CAPABILITY_DB", "")
	t.Setenv("CAPABILITY_DB_DSN", "")
	if _, err := capability.OpenStore(context.Background()); !errors.Is(err, capability.ErrNoStore) {
		t.Fatalf("error = %v, want ErrNoStore", err)
	}
}

// The test double must satisfy the interface the real backends implement.
var _ capability.Store = (*fixture.Memory)(nil)
