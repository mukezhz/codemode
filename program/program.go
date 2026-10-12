// Package program caches the programs a model writes, so a request that has
// been answered once does not pay for generation again.
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

// Key identifies what a cached program answers, and the world it is valid in.
type Key struct {
	Request string
	// Language is the program's dialect, from sandbox.Runner.Language.
	Language string
	// Capabilities fingerprints the capability set the program was written against.
	Capabilities string
	// Caller separates entries that must not be shared; empty means one shared space.
	Caller string
}

// Scope is the partition a Key belongs to; a Store must confine every lookup to one scope.
func (key Key) Scope() string {
	sum := sha256.Sum256([]byte(strings.Join(
		[]string{key.Language, key.Capabilities, key.Caller}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Entry is a cached program returned by a lookup.
type Entry struct {
	// ID identifies this entry to Forget.
	ID     string
	Source string
	// Distance from the looked-up request; zero for exact or unranked caches.
	Distance float64
}

// Cache stores programs by request and returns one for a sufficiently similar
// request. It must be safe for concurrent use.
type Cache interface {
	// Lookup returns a cached program for key; a miss is not an error.
	Lookup(ctx context.Context, key Key) (Entry, bool, error)
	// Put stores source for key, replacing any existing program.
	Put(ctx context.Context, key Key, source string) error
	// Forget drops the entry with this ID; a missing ID is not an error.
	Forget(ctx context.Context, id string) error
	Close() error
}

// Record is one program and the vector its request embedded to.
type Record struct {
	// Scope is from Key.Scope.
	Scope   string
	Request string
	Source  string
	// Vector is the embedding of Request.
	Vector []float32
}

// Hit is a stored record and its distance from the query vector.
type Hit struct {
	ID       string
	Source   string
	Distance float64
}

// Store holds embedded programs and answers nearest-neighbour queries by
// cosine distance; New wraps it into a Cache.
type Store interface {
	// Nearest returns at most limit records in scope, closest first; never records outside scope.
	Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]Hit, error)
	// Put stores record, replacing any with the same scope and request text.
	Put(ctx context.Context, record Record) error
	// Delete drops the record with this ID; a missing ID is not an error.
	Delete(ctx context.Context, id string) error
	Close() error
}

// Options tunes a semantic cache.
type Options struct {
	// Threshold is the largest cosine distance that counts as the same
	// request; zero applies DefaultThreshold.
	Threshold float64
}

// DefaultThreshold is applied when Options.Threshold is zero; strict because a wrong reuse runs the wrong program.
const DefaultThreshold = 0.05

type semanticCache struct {
	store     Store
	embedder  capability.Embedder
	threshold float64
}

// New returns a Cache that finds a program by embedding the request and
// searching store for the nearest one within the threshold. Closing the Cache closes store.
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
