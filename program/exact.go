package program

import (
	"context"
	"strconv"
	"strings"
	"sync"
)

// exactCache reuses a program only for a request it has already seen, matched
// on the text after trimming and case folding.
type exactCache struct {
	mutex   sync.RWMutex
	entries map[string]Entry  // scope + request -> entry
	ids     map[string]string // entry id -> scope + request
	next    int64
}

var _ Cache = (*exactCache)(nil)

// NewExactCache returns a Cache that reuses a program only for a repeat of the
// same request, ignoring surrounding whitespace and letter case.
func NewExactCache() Cache {
	return &exactCache{entries: map[string]Entry{}, ids: map[string]string{}}
}

func (cache *exactCache) entryKey(key Key) string {
	return key.Scope() + "\x00" + strings.ToLower(strings.TrimSpace(key.Request))
}

func (cache *exactCache) Lookup(_ context.Context, key Key) (Entry, bool, error) {
	entryKey := cache.entryKey(key)

	cache.mutex.RLock()
	defer cache.mutex.RUnlock()
	entry, found := cache.entries[entryKey]
	return entry, found, nil
}

func (cache *exactCache) Put(_ context.Context, key Key, source string) error {
	entryKey := cache.entryKey(key)

	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entry, replacing := cache.entries[entryKey]
	if !replacing {
		cache.next++
		entry.ID = strconv.FormatInt(cache.next, 10)
		cache.ids[entry.ID] = entryKey
	}
	entry.Source = source
	cache.entries[entryKey] = entry
	return nil
}

func (cache *exactCache) Forget(_ context.Context, id string) error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entryKey, found := cache.ids[id]
	if !found {
		return nil
	}
	delete(cache.ids, id)
	delete(cache.entries, entryKey)
	return nil
}

func (cache *exactCache) Close() error {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.entries = map[string]Entry{}
	cache.ids = map[string]string{}
	return nil
}
