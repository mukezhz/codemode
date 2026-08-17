// Package program caches the programs a model writes, so a request that has
// been answered once does not pay for generation again.
//
// The expensive part of a code mode request is the model call, not the
// sandbox: generating a program costs on the order of a second, while running
// one costs tens of microseconds. Caching the program therefore removes
// essentially the whole cost of a repeated request, and caching anything
// inside the sandbox removes almost none of it.
//
// # Layers
//
// Cache is the interface the agent depends on. Implement it directly to plug
// in caching of your own — Redis, an HTTP service, an exact-match table, a
// policy that refuses to cache anything touching a write capability.
//
// For the usual semantic cache, implement the smaller Store interface instead
// and wrap it with New: Store does vector storage and nearest-neighbour
// lookup, and New adds embedding, the distance threshold, and scoping. This is
// the same split as capability.Store and capability.Index, and the SQLite and
// Postgres backends provide both.
//
// # Scope
//
// A cached program is only valid for the dialect it was written in, against
// the capability set it was written for, and for the caller it was written on
// behalf of. Key carries all three, and Store implementations must treat a
// lookup as confined to one Key.Scope value. Nothing else is shared between
// scopes.
//
// # Staleness
//
// A cache hit runs code that was written for an earlier state of the world. If
// that code fails, the agent calls Forget and generates a fresh program, so a
// stale entry costs one wasted sandbox run — tens of microseconds — and heals
// itself.
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
	// Request is the user's request, verbatim.
	Request string

	// Language is the dialect the program is written in, from
	// sandbox.Runner.Language. A JavaScript program is meaningless to the Lua
	// engine.
	Language string

	// Capabilities fingerprints the capability set the program was written
	// against. A program that calls a capability that has since been removed
	// must not be reused.
	Capabilities string

	// Caller separates cache entries that must not be shared between callers,
	// such as a role or a tenant. Empty means one shared space.
	Caller string
}

// Scope is the partition a Key belongs to. Two keys share cached programs if
// and only if their scopes are equal, so a Store must confine every lookup to
// one scope value. It is a hash rather than the fields themselves so a backend
// can store it in a single indexed column.
func (key Key) Scope() string {
	sum := sha256.Sum256([]byte(strings.Join(
		[]string{key.Language, key.Capabilities, key.Caller}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Entry is a cached program returned by a lookup.
type Entry struct {
	// ID identifies this entry to Forget. Its form is the cache's business.
	ID string
	// Source is the cached program.
	Source string
	// Distance is how far the stored request was from the one looked up. Zero
	// is an exact match; a cache that does not rank by distance reports zero.
	Distance float64
}

// Cache stores programs by request and returns one for a sufficiently similar
// request. Implementations decide what "sufficiently similar" means.
//
// A Cache must be safe for concurrent use.
type Cache interface {
	// Lookup returns a cached program for key. The bool reports whether one
	// was found; a miss is not an error.
	Lookup(ctx context.Context, key Key) (Entry, bool, error)

	// Put stores source as the program for key. Storing a program for a
	// request that already has one replaces it.
	Put(ctx context.Context, key Key, source string) error

	// Forget drops the entry with this ID. It is called when a cached program
	// fails to run, so a stale entry is not served again. Forgetting an ID
	// that is already gone is not an error.
	Forget(ctx context.Context, id string) error

	// Close releases whatever the cache holds.
	Close() error
}

// Record is one program and the vector its request embedded to.
type Record struct {
	// Scope confines the record, from Key.Scope.
	Scope string
	// Request is the request text the program answers, kept for debugging and
	// for cache inspection.
	Request string
	// Source is the program.
	Source string
	// Vector is the embedding of Request.
	Vector []float32
}

// Hit is a stored record and its distance from the query vector.
type Hit struct {
	ID       string
	Source   string
	Distance float64
}

// Store holds embedded programs and answers nearest-neighbour queries over
// them. It is the part of a semantic cache that is backend-specific; New adds
// everything that is not.
//
// Implementations rank by cosine distance, closest first, matching
// capability.Store.
type Store interface {
	// Nearest returns at most limit records in scope, closest to vector first.
	// Records outside scope are never returned.
	Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]Hit, error)

	// Put stores record, replacing any record in the same scope with the same
	// request text.
	Put(ctx context.Context, record Record) error

	// Delete drops the record with this ID. Deleting an ID that is not present
	// is not an error.
	Delete(ctx context.Context, id string) error

	// Close releases the backend connection.
	Close() error
}

// Options tunes a semantic cache.
type Options struct {
	// Threshold is the largest cosine distance that still counts as the same
	// request. Zero applies DefaultThreshold. Lower is stricter.
	//
	// This is the one number worth tuning per application, because it decides
	// how different two requests may be before one's program is reused for the
	// other. Too high and "delete my todos" is answered by the program written
	// for "list my todos".
	Threshold float64
}

// DefaultThreshold is applied when Options.Threshold is zero. It is deliberately
// strict: a wrong reuse runs the wrong program, while a missed reuse costs only
// the model call that would have happened anyway.
const DefaultThreshold = 0.05

type semanticCache struct {
	store     Store
	embedder  capability.Embedder
	threshold float64
}

// New returns a Cache that finds a program by embedding the request and
// searching store for the nearest one within the distance threshold.
//
// Embedder should be the same one the capability index was built with; the
// cache holds its own vectors, so this is a consistency preference rather than
// a requirement. Closing the Cache closes store.
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
	// One neighbour is enough: anything past the closest is further away, so
	// if the closest misses the threshold they all do.
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
