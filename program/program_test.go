package program

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func wordEmbedder(vocabulary ...string) capability.Embedder {
	index := map[string]int{}
	for position, word := range vocabulary {
		index[word] = position
	}
	return capability.Embedder{
		Model: "test/bag-of-words",
		Embed: func(_ context.Context, text string) ([]float32, error) {
			vector := make([]float32, len(vocabulary))
			for _, word := range strings.Fields(strings.ToLower(text)) {
				position, known := index[word]
				if !known {
					return nil, errors.New("word outside test vocabulary: " + word)
				}
				vector[position] = 1
			}
			return vector, nil
		},
	}
}

var vocabulary = []string{"list", "delete", "complete", "my", "all", "todos", "notes"}

func newCache(t *testing.T, threshold float64) Cache {
	t.Helper()
	cache, err := New(NewMemoryStore(), wordEmbedder(vocabulary...), Options{Threshold: threshold})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() { cache.Close() })
	return cache
}

func key(request string) Key {
	return Key{Request: request, Language: "javascript", Capabilities: "registry-1"}
}

func TestLookupMissesOnAnEmptyCache(t *testing.T) {
	cache := newCache(t, 0)
	if _, found, err := cache.Lookup(context.Background(), key("list my todos")); err != nil || found {
		t.Fatalf("Lookup on empty cache = (found %v, err %v), want (false, nil)", found, err)
	}
}

func TestLookupReturnsTheProgramForTheSameRequest(t *testing.T) {
	ctx := context.Background()
	cache := newCache(t, 0)

	if err := cache.Put(ctx, key("list my todos"), "return 1;"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	entry, found, err := cache.Lookup(ctx, key("list my todos"))
	if err != nil || !found {
		t.Fatalf("Lookup = (found %v, err %v), want (true, nil)", found, err)
	}
	if entry.Source != "return 1;" {
		t.Fatalf("source = %q, want %q", entry.Source, "return 1;")
	}
}

func TestThresholdSeparatesParaphrasesFromDifferentRequests(t *testing.T) {
	ctx := context.Background()
	const threshold = 0.25
	cache := newCache(t, threshold)

	if err := cache.Put(ctx, key("list my todos"), "listing program"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	hits := []string{"list todos"}
	for _, request := range hits {
		entry, found, err := cache.Lookup(ctx, key(request))
		if err != nil {
			t.Fatalf("Lookup(%q) returned error: %v", request, err)
		}
		if !found {
			t.Errorf("Lookup(%q) missed; a paraphrase should reuse the program", request)
			continue
		}
		if entry.Source != "listing program" {
			t.Errorf("Lookup(%q) source = %q", request, entry.Source)
		}
	}

	misses := []string{
		"delete my todos",   // same shape, destructive verb
		"complete my todos", // same shape, different action
		"list my notes",     // same verb, different subject
	}
	for _, request := range misses {
		entry, found, err := cache.Lookup(ctx, key(request))
		if err != nil {
			t.Fatalf("Lookup(%q) returned error: %v", request, err)
		}
		if found {
			t.Errorf("Lookup(%q) hit at distance %v and would run %q; a different request must not reuse a program",
				request, entry.Distance, entry.Source)
		}
	}
}

func TestScopeIsolatesEntries(t *testing.T) {
	ctx := context.Background()
	cache := newCache(t, 0)

	stored := key("list my todos")
	if err := cache.Put(ctx, stored, "javascript program"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	elsewhere := map[string]Key{
		"another dialect":        {Request: stored.Request, Language: "lua", Capabilities: stored.Capabilities},
		"another capability set": {Request: stored.Request, Language: stored.Language, Capabilities: "registry-2"},
		"another caller":         {Request: stored.Request, Language: stored.Language, Capabilities: stored.Capabilities, Caller: "admin"},
	}
	for name, other := range elsewhere {
		if _, found, err := cache.Lookup(ctx, other); err != nil || found {
			t.Errorf("Lookup from %s = (found %v, err %v), want (false, nil)", name, found, err)
		}
	}
	if _, found, _ := cache.Lookup(ctx, stored); !found {
		t.Error("the entry disappeared from its own scope")
	}
}

func TestPutReplacesTheProgramForARequest(t *testing.T) {
	ctx := context.Background()
	cache := newCache(t, 0)

	if err := cache.Put(ctx, key("list my todos"), "old"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if err := cache.Put(ctx, key("list my todos"), "new"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	entry, found, err := cache.Lookup(ctx, key("list my todos"))
	if err != nil || !found {
		t.Fatalf("Lookup = (found %v, err %v), want (true, nil)", found, err)
	}
	if entry.Source != "new" {
		t.Fatalf("source = %q, want %q", entry.Source, "new")
	}
}

func TestForgetDropsAnEntry(t *testing.T) {
	ctx := context.Background()
	cache := newCache(t, 0)

	if err := cache.Put(ctx, key("list my todos"), "stale"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	entry, _, err := cache.Lookup(ctx, key("list my todos"))
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if err := cache.Forget(ctx, entry.ID); err != nil {
		t.Fatalf("Forget returned error: %v", err)
	}
	if _, found, _ := cache.Lookup(ctx, key("list my todos")); found {
		t.Fatal("a forgotten program was served again")
	}
	if err := cache.Forget(ctx, entry.ID); err != nil {
		t.Fatalf("second Forget returned error: %v", err)
	}
}

func TestNewRejectsAnUnusableConfiguration(t *testing.T) {
	embedder := wordEmbedder(vocabulary...)
	cases := map[string]struct {
		store    Store
		embedder capability.Embedder
		options  Options
	}{
		"no store": {nil, embedder, Options{}},
		"no model": {NewMemoryStore(), capability.Embedder{Embed: embedder.Embed}, Options{}},
		"no embed": {NewMemoryStore(), capability.Embedder{Model: "m"}, Options{}},
		"negative": {NewMemoryStore(), embedder, Options{Threshold: -1}},
	}
	for name, test := range cases {
		if _, err := New(test.store, test.embedder, test.options); err == nil {
			t.Errorf("New with %s returned no error", name)
		}
	}
}

func TestExactCacheMatchesOnlyTheSameRequest(t *testing.T) {
	ctx := context.Background()
	cache := NewExactCache()
	defer cache.Close()

	if err := cache.Put(ctx, key("list my todos"), "listing program"); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	entry, found, err := cache.Lookup(ctx, key("  LIST My Todos  "))
	if err != nil || !found {
		t.Fatalf("Lookup of the same request in another case = (found %v, err %v), want (true, nil)", found, err)
	}
	if entry.Source != "listing program" {
		t.Fatalf("source = %q", entry.Source)
	}

	if _, found, _ := cache.Lookup(ctx, key("list todos")); found {
		t.Error("a different request hit the exact cache")
	}

	elsewhere := key("list my todos")
	elsewhere.Caller = "admin"
	if _, found, _ := cache.Lookup(ctx, elsewhere); found {
		t.Error("another caller saw a cached program")
	}

	if err := cache.Forget(ctx, entry.ID); err != nil {
		t.Fatalf("Forget returned error: %v", err)
	}
	if _, found, _ := cache.Lookup(ctx, key("list my todos")); found {
		t.Error("a forgotten program was served again")
	}
	if err := cache.Forget(ctx, entry.ID); err != nil {
		t.Fatalf("second Forget returned error: %v", err)
	}
}
