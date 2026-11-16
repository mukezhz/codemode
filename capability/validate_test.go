package capability

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateRejectsWhatTheSchemaDoesNotDescribe(t *testing.T) {
	item := Capability{
		Input: TypeSchema{Type: "CreateRequest", Fields: map[string]string{
			"title":    "string",
			"priority": "Priority",
			"count":    "integer",
			"ratio":    "number",
			"done":     "bool",
			"tags":     "repeated string",
			"items":    "repeated Item",
			"labels":   "map<string, integer>",
			"meta":     "any",
			"email":    "string",
			"phone":    "string",
		}, Required: []string{"title"}, Oneofs: map[string][]string{"contact": {"email", "phone"}}},
		Types: map[string]TypeSchema{
			"Priority": {Type: "Priority", Values: []string{"low", "high"}},
			"Item":     {Type: "Item", Fields: map[string]string{"id": "integer"}, Required: []string{"id"}},
		},
	}

	for _, test := range []struct{ input, want string }{
		{`{"title":"a","priority":"low","count":2,"ratio":0.5,"done":true,"tags":["x"],"items":[{"id":1}],"labels":{"k":1},"meta":[1,"x"]}`, ""},
		{`{"title":"a","count":1e2,"email":"a@b","phone":null}`, ""},
		{`{}`, "title: required"},
		{`null`, "title: required"},
		{`{"title":null}`, "title: required"},
		{`{"title":"a","nope":1}`, "nope: unknown field; CreateRequest has fields"},
		{`{"title":7}`, "title: want string, got 7"},
		{`{"title":"a","count":1.5}`, "count: want integer, got 1.5"},
		{`{"title":"a","ratio":"1"}`, `ratio: want number, got "1"`},
		{`{"title":"a","done":"yes"}`, `done: want bool, got "yes"`},
		{`{"title":"a","tags":"x"}`, `tags: want list, got "x"`},
		{`{"title":"a","items":[{"id":1},{"id":"2"}]}`, `items[1].id: want integer, got "2"`},
		{`{"title":"a","items":[{}]}`, "items[0].id: required"},
		{`{"title":"a","priority":"urgent"}`, `priority: want one of low, high, got "urgent"`},
		{`{"title":"a","labels":{"k":"v"}}`, `labels["k"]: want integer, got "v"`},
		{`{"title":"a","email":"x","phone":"y"}`, "set at most one of email, phone, got email, phone"},
		{`[]`, "input: want object CreateRequest, got a list"},
		{`{"title":`, "input is not JSON"},
	} {
		err := Validate(item, json.RawMessage(test.input))
		switch {
		case test.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", test.input, err)
		case test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)):
			t.Errorf("%s: error = %v, want it to contain %q", test.input, err, test.want)
		}
	}
}

func TestValidateSkipsAnInputShapeThatWasNeverDeclared(t *testing.T) {
	if err := Validate(Capability{}, json.RawMessage(`{"anything":1}`)); err != nil {
		t.Fatal(err)
	}
	empty := Capability{Input: TypeSchema{Fields: map[string]string{}}}
	if err := Validate(empty, json.RawMessage(`{"anything":1}`)); err == nil {
		t.Fatal("an empty declared shape accepted a field")
	}
}

func TestValidateRejectsAnUndeclaredType(t *testing.T) {
	item := Capability{Input: TypeSchema{Fields: map[string]string{"x": "Missing"}}}
	if err := Validate(item, json.RawMessage(`{"x":{}}`)); err == nil || !strings.Contains(err.Error(), "Missing is not declared") {
		t.Fatalf("error = %v", err)
	}
}

func FuzzValidateNeverPanics(f *testing.F) {
	item := Capability{
		Input: TypeSchema{Fields: map[string]string{"a": "repeated Node", "m": "map<integer, bool>", "n": "integer"}},
		Types: map[string]TypeSchema{"Node": {Fields: map[string]string{"next": "Node", "v": "string"}}},
	}
	for _, seed := range []string{`{}`, `{"a":[{"next":{"v":"x"}}]}`, `{"m":{"1":true,"x":false}}`, `{"n":1e400}`, `[`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_ = Validate(item, json.RawMessage(input))
	})
}
