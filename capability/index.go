package capability

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// EmbedFunc turns text into a vector. Ctx carries cancellation.
type EmbedFunc func(context.Context, string) ([]float32, error)

// Embedder names the embedding model, which the index records to reject a mismatched query model.
type Embedder struct {
	Model string
	Embed EmbedFunc
}

// Match is a capability contract and its distance from the query; lower is closer.
type Match struct {
	Capability Capability `json:"capability"`
	Distance   float64    `json:"distance"`
}

// Index answers semantic capability searches from a Store.
type Index struct {
	store     Store
	embedder  Embedder
	dimension int
}

// Rebuild embeds every capability in registry and replaces the contents of store; the caller closes store.
func Rebuild(ctx context.Context, store Store, registry Registry, embedder Embedder) error {
	if store == nil {
		return errors.New("store is required")
	}
	if len(registry.Capabilities) == 0 {
		return errors.New("registry has no capabilities")
	}
	if err := validateEmbedder(embedder); err != nil {
		return err
	}

	records := make([]Record, len(registry.Capabilities))
	dimension := 0
	for i, capability := range registry.Capabilities {
		vector, err := embedder.Embed(ctx, capability.EmbeddingText())
		if err != nil {
			return fmt.Errorf("embed %s: %w", capability.Name, err)
		}
		if len(vector) == 0 {
			return fmt.Errorf("embed %s: empty vector", capability.Name)
		}
		if dimension == 0 {
			dimension = len(vector)
		} else if len(vector) != dimension {
			return fmt.Errorf("embed %s: got dimension %d, want %d", capability.Name, len(vector), dimension)
		}
		records[i] = Record{Capability: capability, Vector: vector}
	}

	return store.Rebuild(ctx, embedder.Model, dimension, records)
}

// Open reads store's metadata and returns an Index that queries it; closing the Index closes store.
func Open(ctx context.Context, store Store, embedder Embedder) (*Index, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if err := validateEmbedder(embedder); err != nil {
		return nil, err
	}

	model, dimension, err := store.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	if model != embedder.Model {
		return nil, fmt.Errorf("capability index uses embedding model %q, not %q", model, embedder.Model)
	}
	return &Index{store: store, embedder: embedder, dimension: dimension}, nil
}

// Close releases the underlying store.
func (index *Index) Close() error { return index.store.Close() }

// Search returns at most limit capabilities most relevant to query, closest first.
func (index *Index) Search(ctx context.Context, query string, limit int) ([]Match, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit < 1 {
		return nil, errors.New("limit must be positive")
	}

	vector, err := index.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vector) != index.dimension {
		return nil, fmt.Errorf("query embedding has dimension %d, want %d", len(vector), index.dimension)
	}
	return index.store.Search(ctx, vector, limit)
}

func validateEmbedder(embedder Embedder) error {
	if strings.TrimSpace(embedder.Model) == "" {
		return errors.New("embedding model is required")
	}
	if embedder.Embed == nil {
		return errors.New("embed function is required")
	}
	return nil
}
