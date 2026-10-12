package search

import (
	"context"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

func New(registry capability.Registry) codemode.SearchFunc {
	indexed := make([]indexedCapability, 0, len(registry.Capabilities))
	for _, item := range registry.Capabilities {
		indexed = append(indexed, index(item))
	}

	return func(_ context.Context, query string, limit int) ([]capability.Match, error) {
		words := tokenize(query)

		scored := make([]capability.Match, 0, len(indexed))
		unmatched := make([]capability.Match, 0, len(indexed))
		for _, item := range indexed {
			score := item.score(words)
			if score == 0 {
				unmatched = append(unmatched, capability.Match{Capability: item.capability, Distance: 1})
				continue
			}
			// Distance is lower-is-closer, matching capability.Index.
			scored = append(scored, capability.Match{
				Capability: item.capability,
				Distance:   1 / (1 + score),
			})
		}

		sort.SliceStable(scored, func(i, j int) bool {
			if scored[i].Distance != scored[j].Distance {
				return scored[i].Distance < scored[j].Distance
			}
			return scored[i].Capability.Name < scored[j].Capability.Name
		})

		// Top up to the limit with unmatched capabilities: one weak match must not suppress the rest.
		scored = append(scored, unmatched...)
		if limit > 0 && limit < len(scored) {
			scored = scored[:limit]
		}
		return scored, nil
	}
}

type indexedCapability struct {
	capability  capability.Capability
	name        map[string]bool
	description map[string]bool
}

func index(item capability.Capability) indexedCapability {
	return indexedCapability{
		capability:  item,
		name:        stemmedSet(tokenize(split(item.Name))),
		description: stemmedSet(tokenize(item.Description)),
	}
}

func split(name string) string {
	var builder strings.Builder
	for _, symbol := range name {
		switch {
		case symbol == '.' || symbol == '_' || symbol == '-':
			builder.WriteByte(' ')
		case symbol >= 'A' && symbol <= 'Z':
			builder.WriteByte(' ')
			builder.WriteRune(symbol)
		default:
			builder.WriteRune(symbol)
		}
	}
	return builder.String()
}

func (item indexedCapability) score(words []string) float64 {
	score := 0.0
	for _, word := range words {
		root := stem(word)
		switch {
		case item.name[root]:
			score += 3
		case item.description[root]:
			score += 1
		}
	}
	return score
}

// stop words carry no signal; quantity words like "many" would otherwise skew matches.
var stop = set([]string{
	"a", "an", "and", "any", "are", "as", "at", "be", "but", "by", "can", "do",
	"does", "for", "from", "get", "give", "had", "has", "have", "how", "i",
	"in", "is", "it", "me", "much", "my", "of", "on", "or", "please", "show",
	"than", "that", "the", "then", "there", "these", "this", "those", "to",
	"was", "we", "what", "when", "which", "with", "you",
	"many", "lot", "lots", "some", "few", "several", "number", "total",
})

var synonyms = map[string]string{
	"todo":     "task",
	"todos":    "task",
	"item":     "task",
	"items":    "task",
	"work":     "task",
	"ticket":   "task",
	"finished": "complete",
	"done":     "complete",
	"closed":   "complete",
	"late":     "overdue",
	"behind":   "overdue",
	"due":      "overdue",
	"pending":  "open",
	"owner":    "user",
	"person":   "user",
	"people":   "user",
	"team":     "user",
	"who":      "user",
	"summary":  "report",
	"stats":    "report",
	"chart":    "report",
	"trend":    "throughput",
}

func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) <= 1 || stop[field] {
			continue
		}
		if replacement, found := synonyms[field]; found {
			field = replacement
		}
		words = append(words, field)
	}
	return words
}

// stem strips a few suffixes, applied identically to both sides; "complete" drops its "e" so it matches "completed".
func stem(word string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if trimmed, found := strings.CutSuffix(word, suffix); found && len(trimmed) > 2 {
			word = trimmed
			break
		}
	}
	if trimmed, found := strings.CutSuffix(word, "e"); found && len(trimmed) > 2 {
		return trimmed
	}
	return word
}

func set(words []string) map[string]bool {
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[word] = true
	}
	return result
}

func stemmedSet(words []string) map[string]bool {
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[stem(word)] = true
	}
	return result
}
