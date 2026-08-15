package view_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/examples/taskreport/view"
)

// decoded mimics what the sandbox hands back: JSON already decoded into Go.
func decoded(t *testing.T, source string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(source), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestParseAcceptsEveryBlockKind(t *testing.T) {
	document, err := view.Parse(decoded(t, `{
		"title": "This week",
		"blocks": [
			{"type":"stats","items":[{"label":"Open","value":8,"tone":"warn"}]},
			{"type":"bar","series":[{"label":"platform","value":3}]},
			{"type":"line","series":[{"label":"2026-08-15","value":2}]},
			{"type":"table","columns":["Task","Due"],"rows":[["Ship it","2026-08-09"]]},
			{"type":"text","text":"Three tasks are overdue."}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if document.Title != "This week" || len(document.Blocks) != 5 {
		t.Fatalf("document = %+v", document)
	}
}

// A model that answers in prose is not wrong enough to fail on; the reader
// still gets their answer.
func TestParseWrapsAPlainString(t *testing.T) {
	document, err := view.Parse("Nothing is overdue.")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Blocks) != 1 || document.Blocks[0].Type != view.KindText {
		t.Fatalf("document = %+v, want one text block", document)
	}
}

// Every rejection here is one the repair loop can act on, which is why they are
// errors rather than a blank panel in the browser.
func TestParseRejectsMalformedDocuments(t *testing.T) {
	for name, test := range map[string]struct{ source, want string }{
		"unknown block type": {`{"blocks":[{"type":"pie","series":[{"label":"a","value":1}]}]}`, "unknown type"},
		"missing type":       {`{"blocks":[{"text":"hi"}]}`, "needs a type"},
		"invented field":     {`{"blocks":[{"type":"text","text":"hi","colour":"red"}]}`, "colour"},
		"no blocks":          {`{"blocks":[]}`, "no blocks"},
		"empty stats":        {`{"blocks":[{"type":"stats","items":[]}]}`, "needs items"},
		"unlabelled stat":    {`{"blocks":[{"type":"stats","items":[{"label":" ","value":1}]}]}`, "needs a label"},
		"invented tone":      {`{"blocks":[{"type":"stats","items":[{"label":"a","value":1,"tone":"spicy"}]}]}`, "spicy"},
		"empty series":       {`{"blocks":[{"type":"bar","series":[]}]}`, "needs series"},
		"table with no cols": {`{"blocks":[{"type":"table","rows":[["a"]]}]}`, "needs columns"},
		"blank text":         {`{"blocks":[{"type":"text","text":"  "}]}`, "needs text"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := view.Parse(decoded(t, test.source))
			if err == nil {
				t.Fatalf("Parse accepted %s", test.source)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to mention %q", err, test.want)
			}
		})
	}
}

// A ragged row renders as a broken table with no way to tell which cell went
// missing, so it has to fail where the model can still fix it.
func TestParseRejectsARaggedTable(t *testing.T) {
	_, err := view.Parse(decoded(t, `{"blocks":[{"type":"table",
		"columns":["Task","Due"],"rows":[["Ship it","2026-08-09"],["Only one"]]}]}`))
	if err == nil {
		t.Fatal("Parse accepted a row narrower than its columns")
	}
	if !strings.Contains(err.Error(), "1 cells but there are 2 columns") {
		t.Fatalf("error = %v, want it to name the row and the widths", err)
	}
}

func TestParseRejectsNothing(t *testing.T) {
	if _, err := view.Parse(nil); err == nil {
		t.Fatal("Parse accepted a program that returned nothing")
	}
}

// A model with no way to say "I cannot" answers anyway. It reaches for the
// capabilities it does have, gets its own data, and titles it with someone
// else's name — or calls a search capability once per letter of the alphabet to
// rebuild a list it was not given. Both look like answers.
func TestParseAcceptsARefusal(t *testing.T) {
	document, err := view.Parse(decoded(t, `{"unavailable":"I can only see your own tasks."}`))
	if err != nil {
		t.Fatal(err)
	}
	if !document.Refused() {
		t.Fatal("a document with unavailable set does not report itself as a refusal")
	}
	if len(document.Blocks) != 0 {
		t.Fatalf("a refusal carries blocks: %+v", document.Blocks)
	}
}

// Declining and answering at once is the failure worth catching: the reader
// gets whichever the renderer happens to show.
func TestParseRejectsARefusalWithBlocks(t *testing.T) {
	_, err := view.Parse(decoded(t, `{"unavailable":"no","blocks":[{"type":"text","text":"but here is a guess"}]}`))
	if err == nil {
		t.Fatal("Parse accepted a document that both refused and answered")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Fatalf("error = %v", err)
	}
}

// The error for an empty document points at the refusal, so the repair attempt
// is told what to do rather than only what went wrong.
func TestEmptyDocumentSuggestsRefusing(t *testing.T) {
	_, err := view.Parse(decoded(t, `{"blocks":[]}`))
	if err == nil {
		t.Fatal("Parse accepted an empty document")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error = %v, want it to name the refusal field", err)
	}
}

// The instructions have to forbid the two specific improvisations, or the
// refusal is available and unused.
func TestInstructionsForbidImprovising(t *testing.T) {
	for _, want := range []string{
		`{"unavailable":`,
		"Never answer a question about another person",
		"Never call one capability many times with different inputs",
		"Refusing is a correct answer",
	} {
		if !strings.Contains(view.Instructions, want) {
			t.Errorf("the instructions omit %q", want)
		}
	}
}
