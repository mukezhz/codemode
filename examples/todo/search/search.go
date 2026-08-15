package search

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

func New(registry capability.Registry) codemode.SearchFunc {
	// ponytail: keyword ranking keeps the demo dependency-free; use
	// capability.Index.Search when production semantic ranking is wired.
	return func(_ context.Context, query string, limit int) ([]capability.Match, error) {
		query = strings.TrimSpace(strings.ToLower(query))
		if query == "" {
			return nil, errors.New("query is required")
		}
		if limit < 1 {
			return nil, errors.New("limit must be positive")
		}
		words := strings.Fields(query)
		matches := make([]capability.Match, 0, len(registry.Capabilities))
		for _, item := range registry.Capabilities {
			haystack := strings.ToLower(item.Name + " " + item.Description)
			hits := 0
			for _, word := range words {
				if strings.Contains(haystack, strings.Trim(word, ",.")) {
					hits++
				}
			}
			matches = append(matches, capability.Match{Capability: item, Distance: 1 / float64(hits+1)})
		}
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].Distance < matches[j].Distance })
		if limit < len(matches) {
			matches = matches[:limit]
		}
		return matches, nil
	}
}
