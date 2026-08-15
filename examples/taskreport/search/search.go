// Package search ranks capabilities by word overlap with the request.
//
// It exists because this example has no embedding endpoint: DeepSeek publishes
// no embeddings API, and requiring a second provider just to run a demo is a
// poor trade. For a real deployment prefer capability.Index, which ranks by
// meaning rather than by shared words — "what is late?" finds "overdue" there
// and does not here.
//
// What this does get right is the part that matters: search only decides what
// the model is *told about*. It never decides what may run. Ranking badly
// produces a worse answer; it cannot produce an unauthorized call.
package search

import (
	"context"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

// New returns a SearchFunc over registry.
func New(registry capability.Registry) codemode.SearchFunc {
	indexed := make([]indexedCapability, 0, len(registry.Capabilities))
	for _, item := range registry.Capabilities {
		indexed = append(indexed, index(item))
	}

	return func(_ context.Context, query string, limit int) ([]capability.Match, error) {
		words := tokenize(query)

		scored := make([]capability.Match, 0, len(indexed))
		for _, item := range indexed {
			score := item.score(words)
			if score == 0 {
				continue
			}
			// Distance is "lower is closer", so it inverts the score. Staying on
			// that convention means this ranker and capability.Index are
			// interchangeable without the caller noticing.
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

		// Nothing matched: hand back the whole registry rather than nothing.
		// An empty result set means the agent fails outright, and a request
		// phrased in words the descriptions do not use is a ranking failure,
		// not a reason to refuse.
		if len(scored) == 0 {
			for _, item := range indexed {
				scored = append(scored, capability.Match{Capability: item.capability, Distance: 1})
			}
		}
		if limit > 0 && limit < len(scored) {
			scored = scored[:limit]
		}
		return scored, nil
	}
}

type indexedCapability struct {
	capability capability.Capability
	// name holds the words in the capability name, which are the strongest
	// signal: someone asking to "complete a task" means tasks.complete.
	name map[string]bool
	// description holds the words a contract author wrote for a reader.
	description map[string]bool
}

func index(item capability.Capability) indexedCapability {
	return indexedCapability{
		capability:  item,
		name:        stemmedSet(tokenize(split(item.Name))),
		description: stemmedSet(tokenize(item.Description)),
	}
}

// split turns "reports.byProject" into "reports by Project" so that a request
// mentioning a project reaches it. Without this the whole second half of every
// capability name is one unmatchable token, which is exactly the half that says
// what the capability does.
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

// score weights a name hit above a description hit. Both sides are stemmed at
// index time, so "reports" in a name and "report" in a request are the same
// token by the time they are compared.
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

// stop words carry no signal about which capability is wanted, and matching
// them would make every capability score alike.
var stop = set([]string{
	"a", "an", "and", "any", "are", "as", "at", "be", "but", "by", "can", "do",
	"for", "from", "give", "how", "i", "in", "is", "it", "me", "my", "of", "on",
	"or", "please", "show", "than", "that", "the", "then", "there", "this",
	"to", "was", "what", "when", "which", "with", "you",
})

func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) > 1 && !stop[field] {
			words = append(words, field)
		}
	}
	return words
}

// stem is deliberately crude: it strips the few suffixes that separate a
// request's phrasing from a contract's. A real stemmer would be better and a
// real embedder better still.
func stem(word string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if trimmed, found := strings.CutSuffix(word, suffix); found && len(trimmed) > 2 {
			return trimmed
		}
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

// stemmedSet indexes the stem, so stemming happens once here rather than on
// every query against every capability.
func stemmedSet(words []string) map[string]bool {
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[stem(word)] = true
	}
	return result
}
