package capability

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
)

// memoryStore is an in-memory Store, so the Index logic is testable without a
// database or a vector extension. It ranks by cosine distance, as the real
// backends do.
type memoryStore struct {
	model     string
	dimension int
	records   []Record
	closed    bool
}

func (store *memoryStore) Rebuild(_ context.Context, model string, dimension int, records []Record) error {
	store.model, store.dimension, store.records = model, dimension, records
	return nil
}

func (store *memoryStore) Metadata(context.Context) (string, int, error) {
	if store.model == "" {
		return "", 0, errors.New("index has not been built")
	}
	return store.model, store.dimension, nil
}

func (store *memoryStore) Search(_ context.Context, vector []float32, limit int) ([]Match, error) {
	matches := make([]Match, 0, len(store.records))
	for _, record := range store.records {
		matches = append(matches, Match{
			Capability: record.Capability,
			Distance:   cosineDistance(vector, record.Vector),
		})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Distance < matches[j].Distance })
	if limit < len(matches) {
		matches = matches[:limit]
	}
	return matches, nil
}

func (store *memoryStore) Close() error {
	store.closed = true
	return nil
}

func cosineDistance(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 1
	}
	return 1 - dot/(sqrt(normA)*sqrt(normB))
}

func sqrt(value float64) float64 {
	// Newton's method keeps this file free of an import used once.
	guess := value
	for range 20 {
		guess = (guess + value/guess) / 2
	}
	return guess
}

func testEmbedder() Embedder {
	return Embedder{Model: "test-embedding-v1", Embed: func(_ context.Context, text string) ([]float32, error) {
		text = strings.ToLower(text)
		return []float32{
			boolFloat(strings.Contains(text, "student") || strings.Contains(text, "interview")),
			boolFloat(strings.Contains(text, "invoice") || strings.Contains(text, "unpaid") || strings.Contains(text, "bill")),
			boolFloat(strings.Contains(text, "email")),
		}, nil
	}}
}

func testRegistry() Registry {
	return Registry{Capabilities: []Capability{
		{Name: "students.interviewsWithin", RPC: "student.v1.StudentService/InterviewsWithin", Description: "Returns upcoming student interviews.", Input: TypeSchema{Fields: map[string]string{}}, Output: TypeSchema{Fields: map[string]string{}}},
		{Name: "invoices.listUnpaid", RPC: "invoice.v1.InvoiceService/ListUnpaid", Description: "Returns unpaid invoices.", Input: TypeSchema{Fields: map[string]string{}}, Output: TypeSchema{Fields: map[string]string{}}},
		{Name: "email.send", RPC: "email.v1.EmailService/Send", Description: "Sends an email.", Input: TypeSchema{Fields: map[string]string{}}, Output: TypeSchema{Fields: map[string]string{}}},
	}}
}

func TestIndexRanksBySimilarity(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	embedder := testEmbedder()

	if err := Rebuild(ctx, store, testRegistry(), embedder); err != nil {
		t.Fatalf("Rebuild returned error: %v", err)
	}
	if store.dimension != 3 || store.model != "test-embedding-v1" {
		t.Fatalf("metadata = %q/%d, want test-embedding-v1/3", store.model, store.dimension)
	}

	index, err := Open(ctx, store, embedder)
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

	if err := index.Close(); err != nil || !store.closed {
		t.Fatalf("Close did not reach the store (err %v, closed %v)", err, store.closed)
	}
}

func TestOpenRejectsAMismatchedEmbeddingModel(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	if err := Rebuild(ctx, store, testRegistry(), testEmbedder()); err != nil {
		t.Fatal(err)
	}

	other := testEmbedder()
	other.Model = "different-embedding-v2"
	_, err := Open(ctx, store, other)
	if err == nil {
		t.Fatal("Open returned no error for a mismatched embedding model")
	}
	if !strings.Contains(err.Error(), "different-embedding-v2") {
		t.Fatalf("error = %v, want it to name the requested model", err)
	}
}

func TestRebuildRejectsRaggedVectors(t *testing.T) {
	first := true
	embedder := Embedder{Model: "ragged", Embed: func(context.Context, string) ([]float32, error) {
		if first {
			first = false
			return []float32{1, 0, 0}, nil
		}
		return []float32{1, 0}, nil
	}}

	err := Rebuild(context.Background(), &memoryStore{}, testRegistry(), embedder)
	if err == nil {
		t.Fatal("Rebuild returned no error for vectors of differing dimension")
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error = %v, want a dimension mismatch", err)
	}
}

func TestOpenStoreSelectsTheBackendFromTheEnvironment(t *testing.T) {
	for name, test := range map[string]struct {
		env  map[string]string
		want string // substring of the expected error
	}{
		// Both backends fail to connect in a unit test; the assertion is that
		// the right backend was selected before it tried.
		"sqlite needs the extension": {
			map[string]string{"CAPABILITY_DB": "sqlite", "CAPABILITY_DB_DSN": "capabilities.db"},
			"sqlite-vector extension",
		},
		"sqlite needs a dsn": {
			map[string]string{"CAPABILITY_DB": "sqlite"},
			"CAPABILITY_DB_DSN must be the SQLite database path",
		},
		"postgres needs a dsn": {
			map[string]string{"CAPABILITY_DB": "postgres"},
			"CAPABILITY_DB_DSN must be the Postgres connection URL",
		},
		"unknown backend": {
			map[string]string{"CAPABILITY_DB": "mysql", "CAPABILITY_DB_DSN": "x"},
			"is not supported",
		},
		"unset backend needs a dsn": {
			map[string]string{},
			"set CAPABILITY_DB_DSN",
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, key := range []string{"CAPABILITY_DB", "CAPABILITY_DB_DSN", "SQLITE_VECTOR_EXTENSION"} {
				t.Setenv(key, test.env[key])
			}
			store, err := OpenStore(context.Background())
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

func TestFormatVectorMatchesPgvectorText(t *testing.T) {
	if got := formatVector([]float32{1, -0.5, 0}); got != "[1,-0.5,0]" {
		t.Fatalf("formatVector() = %q, want %q", got, "[1,-0.5,0]")
	}
	if got := formatVector(nil); got != "[]" {
		t.Fatalf("formatVector(nil) = %q, want %q", got, "[]")
	}
}

// Both backends and the test double must satisfy the interface.
var _ Store = (*memoryStore)(nil)
