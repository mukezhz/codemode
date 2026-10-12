package view_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/examples/taskreport/view"
)

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

func TestParseWrapsAPlainString(t *testing.T) {
	document, err := view.Parse("Nothing is overdue.")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Blocks) != 1 || document.Blocks[0].Type != view.KindText {
		t.Fatalf("document = %+v, want one text block", document)
	}
}

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

func TestParseRejectsARefusalWithBlocks(t *testing.T) {
	_, err := view.Parse(decoded(t, `{"unavailable":"no","blocks":[{"type":"text","text":"but here is a guess"}]}`))
	if err == nil {
		t.Fatal("Parse accepted a document that both refused and answered")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Fatalf("error = %v", err)
	}
}

func TestEmptyDocumentSuggestsRefusing(t *testing.T) {
	_, err := view.Parse(decoded(t, `{"blocks":[]}`))
	if err == nil {
		t.Fatal("Parse accepted an empty document")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error = %v, want it to name the refusal field", err)
	}
}

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
