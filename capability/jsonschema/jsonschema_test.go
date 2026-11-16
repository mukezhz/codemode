package jsonschema

import (
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func TestObjectConvertsEveryKind(t *testing.T) {
	types := map[string]capability.TypeSchema{}
	got, err := Object(map[string]any{
		"type":     "object",
		"required": []any{"id", "state"},
		"properties": map[string]any{
			"id":     map[string]any{"type": "integer", "description": "issue number"},
			"score":  map[string]any{"type": []any{"number", "null"}},
			"open":   map[string]any{"type": "boolean"},
			"state":  map[string]any{"type": "string", "enum": []any{"open", "closed"}},
			"labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"author": map[string]any{"type": "object", "properties": map[string]any{"login": map[string]any{"type": "string"}}},
		},
	}, "Issue", types)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"id": "integer", "score": "number", "open": "bool", "state": "IssueState",
		"labels": "repeated string", "author": "IssueAuthor"}
	for field, kind := range want {
		if got.Fields[field] != kind {
			t.Errorf("%s = %q, want %q", field, got.Fields[field], kind)
		}
	}
	if strings.Join(got.Required, ",") != "id,state" || strings.Join(got.Optional, ",") != "author,labels,open,score" {
		t.Errorf("required = %v, optional = %v", got.Required, got.Optional)
	}
	if got.Descriptions["id"] != "issue number" {
		t.Errorf("descriptions = %v", got.Descriptions)
	}
	if strings.Join(types["IssueState"].Values, ",") != "open,closed" || types["IssueAuthor"].Fields["login"] != "string" {
		t.Errorf("types = %+v", types)
	}
}

func TestObjectRefusesWhatItCannotExpress(t *testing.T) {
	for name, raw := range map[string]any{
		"not an object": map[string]any{"type": "string"},
		"oneOf":         map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"oneOf": []any{}}}},
		"two types":     map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": []any{"string", "number"}}}},
		"number enum":   map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string", "enum": []any{1}}}},
	} {
		if _, err := Object(raw, "T", map[string]capability.TypeSchema{}); err == nil {
			t.Errorf("%s: converted", name)
		}
	}
}
