package program

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/mukezhz/codemode/capability"
)

type Key struct {
	Request      string
	Language     string
	Capabilities string
	// Empty means one shared space.
	Caller string
}

// A Store must confine every lookup to one scope.
func (key Key) Scope() string {
	sum := sha256.Sum256([]byte(strings.Join(
		[]string{key.Language, key.Capabilities, key.Caller}, "\x00")))
	return hex.EncodeToString(sum[:])
}

type Entry struct {
	ID     string
	Source string
	// Zero for exact or unranked caches.
	Distance float64
}

// It must be safe for concurrent use.
type Cache interface {
	// A miss is not an error.
	Lookup(ctx context.Context, key Key) (Entry, bool, error)
	Put(ctx context.Context, key Key, source string) error
	// A missing ID is not an error.
	Forget(ctx context.Context, id string) error
	Close() error
}

type Record struct {
	Scope   string
	Request string
	Source  string
	Vector  []float32
}

type Hit struct {
	ID       string
	Source   string
	Distance float64
}

type Store interface {
	// Never returns records outside scope.
	Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]Hit, error)
	Put(ctx context.Context, record Record) error
	// A missing ID is not an error.
	Delete(ctx context.Context, id string) error
	Close() error
}

type Options struct {
	Threshold float64
}

// Strict because a wrong reuse runs the wrong program.
const DefaultThreshold = 0.05

type semanticCache struct {
	store     Store
	embedder  capability.Embedder
	threshold float64
}

var _ Cache = (*semanticCache)(nil)

// Closing the Cache closes store.
func New(store Store, embedder capability.Embedder, options Options) (Cache, error) {
	if store == nil {
		return nil, errors.New("store is required")
	}
	if strings.TrimSpace(embedder.Model) == "" || embedder.Embed == nil {
		return nil, errors.New("embedder needs a model name and an embed function")
	}
	if options.Threshold < 0 {
		return nil, errors.New("threshold must not be negative")
	}
	if options.Threshold == 0 {
		options.Threshold = DefaultThreshold
	}
	return &semanticCache{store: store, embedder: embedder, threshold: options.Threshold}, nil
}

func (cache *semanticCache) Lookup(ctx context.Context, key Key) (Entry, bool, error) {
	request := strings.TrimSpace(key.Request)
	if request == "" {
		return Entry{}, false, errors.New("request is required")
	}
	vector, err := cache.embedder.Embed(ctx, request)
	if err != nil {
		return Entry{}, false, fmt.Errorf("embed request: %w", err)
	}
	hits, err := cache.store.Nearest(ctx, key.Scope(), vector, 1)
	if err != nil {
		return Entry{}, false, err
	}
	if len(hits) == 0 || hits[0].Distance > cache.threshold {
		return Entry{}, false, nil
	}
	return Entry{ID: hits[0].ID, Source: hits[0].Source, Distance: hits[0].Distance}, true, nil
}

func (cache *semanticCache) Put(ctx context.Context, key Key, source string) error {
	request := strings.TrimSpace(key.Request)
	if request == "" {
		return errors.New("request is required")
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("source is required")
	}
	vector, err := cache.embedder.Embed(ctx, request)
	if err != nil {
		return fmt.Errorf("embed request: %w", err)
	}
	return cache.store.Put(ctx, Record{
		Scope:   key.Scope(),
		Request: request,
		Source:  source,
		Vector:  vector,
	})
}

func (cache *semanticCache) Forget(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return cache.store.Delete(ctx, id)
}

func (cache *semanticCache) Close() error { return cache.store.Close() }
