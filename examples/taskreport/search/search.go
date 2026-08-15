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
		unmatched := make([]capability.Match, 0, len(indexed))
		for _, item := range indexed {
			score := item.score(words)
			if score == 0 {
				unmatched = append(unmatched, capability.Match{Capability: item.capability, Distance: 1})
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

		// Top up to the limit with whatever did not match.
		//
		// This is the important part, and it was missing. Ranking should decide
		// the *order* a model sees capabilities in, not whether it sees enough
		// of them to answer. One weak accidental match used to suppress
		// everything else — "how many todos do I have" matched the word "many"
		// in one description and offered that capability alone, so the model
		// dutifully answered a question nobody asked.
		//
		// A model handed twelve capabilities and needing three will ignore nine.
		// A model handed one will use the one.
		scored = append(scored, unmatched...)
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
//
// The quantity words matter more than they look. "how many" appears in the
// description of a capability that counts one specific thing, so leaving "many"
// in made every counting question match that one capability and nothing else.
var stop = set([]string{
	"a", "an", "and", "any", "are", "as", "at", "be", "but", "by", "can", "do",
	"does", "for", "from", "get", "give", "had", "has", "have", "how", "i",
	"in", "is", "it", "me", "much", "my", "of", "on", "or", "please", "show",
	"than", "that", "the", "then", "there", "these", "this", "those", "to",
	"was", "we", "what", "when", "which", "with", "you",
	// Quantity words. A request is almost always asking "how many" of
	// something, so they separate no two capabilities.
	"many", "lot", "lots", "some", "few", "several", "number", "total",
})

// synonyms map what a person says to what the contracts say. The gap is real
// and it is the application's to close: this app calls them tasks, and half its
// users will call them todos.
//
// An embedding index closes it without a table — "what is late?" finds
// "overdue" there. This is the honest cost of ranking by shared words.
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

// stem is deliberately crude: it strips the few suffixes that separate a
// request's phrasing from a contract's. A real stemmer would be better and a
// real embedder better still.
//
// The trailing "e" matters more than it looks. Stripping "ed" from "completed"
// gives "complet", so leaving "complete" alone meant the two never matched —
// a request saying "finished work" reached tasks.complete and never
// reports.completedBetween. Applying the same rule to both sides is what makes
// a crude stemmer usable; applying it to one is worse than not stemming.
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

// stemmedSet indexes the stem, so stemming happens once here rather than on
// every query against every capability.
func stemmedSet(words []string) map[string]bool {
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[stem(word)] = true
	}
	return result
}
