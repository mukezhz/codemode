// Package view is the contract between the model-authored program and the
// browser.
//
// A program that returns a sentence can only ever produce a sentence. Returning
// a small document instead — a few stat cards, a bar chart, a table — lets one
// prompt produce a report the user can actually read, and lets the frontend
// render it without knowing anything about tasks.
//
// The schema is deliberately tiny. Every block type here is one React component
// on the other side, and a model can hold the whole vocabulary in its head:
//
//	{"title": "This week", "blocks": [
//	  {"type": "stats", "items": [{"label": "Open", "value": 8}]},
//	  {"type": "bar", "series": [{"label": "platform", "value": 3}]},
//	  {"type": "table", "columns": ["Task", "Due"], "rows": [["Ship it", "2026-08-09"]]},
//	  {"type": "text", "text": "Three tasks are overdue."}
//	]}
//
// Parse is strict. A model that invents a block type or misspells a field is
// told so while the error can still be fed back for repair, rather than the
// browser rendering a blank panel.
package view

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Block kinds. Each is one component in the frontend renderer.
const (
	// KindStats is a row of headline numbers.
	KindStats = "stats"
	// KindBar is a categorical comparison: work per project, per person.
	KindBar = "bar"
	// KindLine is a value over time, such as completions per day.
	KindLine = "line"
	// KindTable is rows the reader needs to see individually.
	KindTable = "table"
	// KindText is a sentence of interpretation between the figures.
	KindText = "text"
)

// Document is what a program returns and the browser renders.
type Document struct {
	Title  string  `json:"title,omitempty"`
	Blocks []Block `json:"blocks,omitempty"`

	// Unavailable explains, in one sentence, why the request cannot be
	// answered with the capabilities this caller has. A document that sets it
	// carries no blocks.
	//
	// Without somewhere to say "I cannot", a model answers anyway. Asked for a
	// colleague's tasks it will call the capabilities it does have, get its own
	// rows, and title them with the colleague's name. Asked for every task when
	// it has only a search, it will call search thirty-six times, once per
	// letter and digit, and reassemble the list.
	//
	// Both produce a plausible report. Neither answers the question. A refusal
	// is the honest output and it has to be reachable, or the model will find a
	// worse way to fill the silence.
	Unavailable string `json:"unavailable,omitempty"`
}

// Block is one element of a document. Which fields matter depends on Type,
// which is why this is one struct rather than an interface: it decodes from
// JSON in one step, and the validation below is what keeps it honest.
type Block struct {
	Type string `json:"type"`

	// Heading labels the block. Optional everywhere.
	Heading string `json:"heading,omitempty"`

	// Items carries a stats block.
	Items []Stat `json:"items,omitempty"`

	// Series carries a bar or line block.
	Series []Point `json:"series,omitempty"`

	// Columns and Rows carry a table. Every row must be as wide as Columns.
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`

	// Text carries a text block.
	Text string `json:"text,omitempty"`
}

// Stat is one headline number.
type Stat struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	// Tone tints the card: "good", "warn", "bad", or empty for neutral.
	Tone string `json:"tone,omitempty"`
}

// Point is one bar, or one position on a line.
type Point struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// Tones a Stat may carry.
var tones = map[string]bool{"": true, "good": true, "warn": true, "bad": true}

// Parse decodes and validates a document produced by a program.
//
// The value arrives as the Go form of decoded JSON — map[string]any and
// friends — because that is what the sandbox hands back. Re-encoding it to
// route through the same decoder keeps one definition of the schema rather than
// two that can drift.
func Parse(value any) (Document, error) {
	if value == nil {
		return Document{}, fmt.Errorf("the program returned nothing; return a view document")
	}
	if text, ok := value.(string); ok {
		// A model that returns prose instead of a document is not wrong enough
		// to fail on: wrap it, and the reader still gets their answer.
		return Document{Blocks: []Block{{Type: KindText, Text: text}}}, nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return Document{}, fmt.Errorf("encode program result: %w", err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("the program's result is not a view document: %w", err)
	}
	if err := document.Validate(); err != nil {
		return Document{}, err
	}
	return document, nil
}

// Refused reports whether the document is a refusal rather than an answer.
func (document Document) Refused() bool { return strings.TrimSpace(document.Unavailable) != "" }

// Validate reports the first problem with a document.
func (document Document) Validate() error {
	if document.Refused() {
		// A refusal with blocks is the failure worth catching: it means the
		// model both declined and answered, and the reader gets whichever the
		// renderer happens to show.
		if len(document.Blocks) > 0 {
			return fmt.Errorf("a document may say it is unavailable or carry blocks, not both")
		}
		return nil
	}
	if len(document.Blocks) == 0 {
		return fmt.Errorf(`the document has no blocks; if you cannot answer, set "unavailable" instead`)
	}
	for index, block := range document.Blocks {
		if err := block.validate(); err != nil {
			return fmt.Errorf("block %d (%s): %w", index, block.Type, err)
		}
	}
	return nil
}

func (block Block) validate() error {
	switch block.Type {
	case KindStats:
		if len(block.Items) == 0 {
			return fmt.Errorf("needs items")
		}
		for _, item := range block.Items {
			if strings.TrimSpace(item.Label) == "" {
				return fmt.Errorf("every item needs a label")
			}
			if !tones[item.Tone] {
				return fmt.Errorf("tone %q is not good, warn, or bad", item.Tone)
			}
		}

	case KindBar, KindLine:
		if len(block.Series) == 0 {
			return fmt.Errorf("needs series")
		}
		for _, point := range block.Series {
			if strings.TrimSpace(point.Label) == "" {
				return fmt.Errorf("every point needs a label")
			}
		}

	case KindTable:
		if len(block.Columns) == 0 {
			return fmt.Errorf("needs columns")
		}
		// A ragged row renders as a broken table, and the reader has no way to
		// tell which cell went missing.
		for row, cells := range block.Rows {
			if len(cells) != len(block.Columns) {
				return fmt.Errorf("row %d has %d cells but there are %d columns",
					row, len(cells), len(block.Columns))
			}
		}

	case KindText:
		if strings.TrimSpace(block.Text) == "" {
			return fmt.Errorf("needs text")
		}

	case "":
		return fmt.Errorf("needs a type")

	default:
		return fmt.Errorf("unknown type; use %s", strings.Join(
			[]string{KindStats, KindBar, KindLine, KindTable, KindText}, ", "))
	}
	return nil
}

// Instructions tell the model to return a document. They are appended to the
// engine's own system prompt through agent.Options.Instructions, so the rules
// about the language and the single binding stay where they belong.
const Instructions = `Return a view document, not a sentence. It is an object with an optional "title" and a "blocks" array. Each block is one of:

{"type":"stats","heading":"...","items":[{"label":"Open","value":8,"tone":"warn"}]}   tone is good, warn, bad, or omitted
{"type":"bar","heading":"...","series":[{"label":"platform","value":3}]}              a comparison across categories
{"type":"line","heading":"...","series":[{"label":"2026-08-15","value":2}]}           a value over time, oldest first
{"type":"table","heading":"...","columns":["Task","Due"],"rows":[["Ship it","2026-08-09"]]}
{"type":"text","text":"Three tasks are overdue."}                                     a sentence of interpretation

Rules for the document:
- Lead with a stats block when the answer involves totals.
- Use a chart when comparing categories or showing a trend; use a table when the reader needs individual rows.
- Every table row must have exactly as many cells as there are columns, and every cell must be a string.
- Add a short text block explaining what the figures mean. Do not narrate what you called.
- Use tone to draw the eye: bad for overdue, warn for at risk, good for finished work.
- No other block types and no other fields exist. An unknown one is rejected.

If the capabilities above cannot answer the request as asked, return this instead, and nothing else:

{"unavailable":"one sentence saying what you cannot do"}

Refuse rather than approximate. In particular:
- Never answer a question about another person, project, or period using data that is not about it. Returning your own figures under their name is worse than refusing.
- Never call one capability many times with different inputs to rebuild what a capability you were not given would have returned. Searching every letter of the alphabet to reconstruct a list is not a substitute for a list.
- Never title a document as being about something the figures are not about.
- A capability call that fails is not something to work around. Let it fail, or refuse.

Refusing is a correct answer. Guessing is not.`
