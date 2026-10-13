package capability

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// Keyword ranks capabilities by how many query words appear in their name and
// description. It needs no database or embeddings, so it is the search to start
// with; switch to an Index when the registry outgrows word matching.
// ponytail: substring hits, no stemming or weighting; an Index is the upgrade.
func Keyword(registry Registry) func(ctx context.Context, query string, limit int) ([]Match, error) {
	return func(_ context.Context, query string, limit int) ([]Match, error) {
		query = strings.TrimSpace(strings.ToLower(query))
		if query == "" {
			return nil, errors.New("query is required")
		}
		if limit < 1 {
			return nil, errors.New("limit must be positive")
		}
		words := strings.Fields(query)
		matches := make([]Match, 0, len(registry.Capabilities))
		for _, item := range registry.Capabilities {
			haystack := strings.ToLower(item.Name + " " + item.Description)
			hits := 0
			for _, word := range words {
				if strings.Contains(haystack, strings.Trim(word, ",.?!")) {
					hits++
				}
			}
			matches = append(matches, Match{Capability: item, Distance: 1 / float64(hits+1)})
		}
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].Distance < matches[j].Distance })
		return matches[:min(limit, len(matches))], nil
	}
}
