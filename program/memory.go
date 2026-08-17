package program

import (
	"context"
	"math"
	"sort"
	"strconv"
	"sync"
)

// memoryStore is a Store held in the process.
//
// ponytail: a full scan over a map. A process that has answered enough distinct
// requests for that to matter has a database; use the SQLite or Postgres
// backend there.
type memoryStore struct {
	mutex   sync.RWMutex
	records map[string]Record
	next    int64
}

// NewMemoryStore returns a Store held in memory, for tests, single-process
// tools, and any deployment that is happy to lose the cache on restart. Wrap it
// with New like any other Store.
func NewMemoryStore() Store {
	return &memoryStore{records: map[string]Record{}}
}

func (store *memoryStore) Nearest(ctx context.Context, scope string, vector []float32, limit int) ([]Hit, error) {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	hits := make([]Hit, 0, len(store.records))
	for id, record := range store.records {
		if record.Scope != scope {
			continue
		}
		distance, ok := cosineDistance(vector, record.Vector)
		if !ok {
			continue
		}
		hits = append(hits, Hit{ID: id, Source: record.Source, Distance: distance})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Distance < hits[j].Distance })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (store *memoryStore) Put(ctx context.Context, record Record) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	for id, existing := range store.records {
		if existing.Scope == record.Scope && existing.Request == record.Request {
			store.records[id] = record
			return nil
		}
	}
	store.next++
	store.records[strconv.FormatInt(store.next, 10)] = record
	return nil
}

func (store *memoryStore) Delete(ctx context.Context, id string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.records, id)
	return nil
}

func (store *memoryStore) Close() error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.records = map[string]Record{}
	return nil
}

// cosineDistance is 1 - cosine similarity, the same ranking the SQL backends
// ask their vector extension for. The bool is false when the vectors cannot be
// compared, which is treated as no match rather than as an error: a mismatched
// dimension means the entry was written by a different embedding model.
func cosineDistance(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0, false
	}
	return 1 - dot/(math.Sqrt(normA)*math.Sqrt(normB)), true
}
