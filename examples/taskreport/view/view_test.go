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
