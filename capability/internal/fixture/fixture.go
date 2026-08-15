// Package fixture holds test doubles shared by the capability package and its
// store backends. It is internal: nothing here is part of the library's API.
package fixture

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/mukezhz/code-mode/capability"
)

// Memory is an in-memory Store, so the Index logic is testable without a
// database or a vector extension. It ranks by cosine distance, as the real
// backends do.
type Memory struct {
	model     string
	dimension int
	records   []capability.Record
	Closed    bool
}

func (store *Memory) Rebuild(_ context.Context, model string, dimension int, records []capability.Record) error {
	store.model, store.dimension, store.records = model, dimension, records
	return nil
}

func (store *Memory) Metadata(context.Context) (string, int, error) {
	if store.model == "" {
		return "", 0, errors.New("index has not been built")
	}
	return store.model, store.dimension, nil
}

func (store *Memory) Search(_ context.Context, vector []float32, limit int) ([]capability.Match, error) {
	matches := make([]capability.Match, 0, len(store.records))
	for _, record := range store.records {
		matches = append(matches, capability.Match{
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

func (store *Memory) Close() error {
	store.Closed = true
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

func Embedder() capability.Embedder {
	return capability.Embedder{Model: "test-embedding-v1", Embed: func(_ context.Context, text string) ([]float32, error) {
		text = strings.ToLower(text)
		return []float32{
			boolFloat(strings.Contains(text, "student") || strings.Contains(text, "interview")),
			boolFloat(strings.Contains(text, "invoice") || strings.Contains(text, "unpaid") || strings.Contains(text, "bill")),
			boolFloat(strings.Contains(text, "email")),
		}, nil
	}}
}

func Registry() capability.Registry {
	return capability.Registry{Capabilities: []capability.Capability{
		{Name: "students.interviewsWithin", RPC: "student.v1.StudentService/InterviewsWithin", Description: "Returns upcoming student interviews.", Input: capability.TypeSchema{Fields: map[string]string{}}, Output: capability.TypeSchema{Fields: map[string]string{}}},
		{Name: "invoices.listUnpaid", RPC: "invoice.v1.InvoiceService/ListUnpaid", Description: "Returns unpaid invoices.", Input: capability.TypeSchema{Fields: map[string]string{}}, Output: capability.TypeSchema{Fields: map[string]string{}}},
		{Name: "email.send", RPC: "email.v1.EmailService/Send", Description: "Sends an email.", Input: capability.TypeSchema{Fields: map[string]string{}}, Output: capability.TypeSchema{Fields: map[string]string{}}},
	}}
}

func boolFloat(value bool) float32 {
	if value {
		return 1
	}
	return 0
}
