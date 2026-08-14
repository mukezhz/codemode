package capability

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddingTextIncludesContract(t *testing.T) {
	text := embeddingText(Capability{
		Name:        "students.interviewsWithin",
		RPC:         "student.v1.StudentService/InterviewsWithin",
		Description: "Returns upcoming interviews.",
		Input:       TypeSchema{Type: "Request", Fields: map[string]string{"days": "number"}},
		Output:      TypeSchema{Type: "Response", Fields: map[string]string{"students": "Student[]"}},
	})
	for _, want := range []string{"students.interviewsWithin", "upcoming interviews", `"days":"number"`, `"students":"Student[]"`} {
		if !strings.Contains(text, want) {
			t.Errorf("embedding text does not contain %q", want)
		}
	}
}

func TestVectorSearch(t *testing.T) {
	extension := os.Getenv("SQLITE_VECTOR_EXTENSION")
	if extension == "" {
		t.Skip("SQLITE_VECTOR_EXTENSION is not set")
	}

	embedder := Embedder{Model: "test-embedding-v1", Embed: func(_ context.Context, text string) ([]float32, error) {
		text = strings.ToLower(text)
		return []float32{
			boolFloat(strings.Contains(text, "student") || strings.Contains(text, "interview")),
			boolFloat(strings.Contains(text, "invoice") || strings.Contains(text, "unpaid")),
			boolFloat(strings.Contains(text, "email")),
		}, nil
	}}
	registry := Registry{Capabilities: []Capability{
		{Name: "students.interviewsWithin", RPC: "student.v1.StudentService/InterviewsWithin", Description: "Returns upcoming student interviews.", Input: TypeSchema{Fields: map[string]string{}}, Output: TypeSchema{Fields: map[string]string{}}},
		{Name: "invoices.listUnpaid", RPC: "invoice.v1.InvoiceService/ListUnpaid", Description: "Returns unpaid invoices.", Input: TypeSchema{Fields: map[string]string{}}, Output: TypeSchema{Fields: map[string]string{}}},
		{Name: "email.send", RPC: "email.v1.EmailService/Send", Description: "Sends an email.", Input: TypeSchema{Fields: map[string]string{}}, Output: TypeSchema{Fields: map[string]string{}}},
	}}

	path := filepath.Join(t.TempDir(), "capabilities.db")
	if err := Rebuild(context.Background(), path, extension, registry, embedder); err != nil {
		t.Fatal(err)
	}
	index, err := Open(context.Background(), path, extension, embedder)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	matches, err := index.Search(context.Background(), "find unpaid bills", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Capability.Name != "invoices.listUnpaid" {
		t.Fatalf("matches = %#v", matches)
	}
}

func boolFloat(value bool) float32 {
	if value {
		return 1
	}
	return 0
}
