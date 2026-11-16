package capability

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type priority string

func (priority) Enum() []string { return []string{"low", "high"} }

type audit struct {
	CreatedAt time.Time `json:"created_at"`
}

type tag struct {
	Name string `json:"name"`
}

type createTask struct {
	audit
	Title    string          `json:"title" desc:"short summary"`
	Priority priority        `json:"priority,omitempty"`
	Due      *time.Time      `json:"due"`
	Points   int             `json:"points"`
	Weight   float64         `json:"weight,omitzero"`
	ID       int64           `json:"id,string,omitempty"`
	Tags     []tag           `json:"tags"`
	Counts   map[string]int  `json:"counts"`
	Extra    json.RawMessage `json:"extra,omitempty"`
	Blob     []byte          `json:"blob,omitempty"`
	Hidden   string          `json:"-"`
	Untagged bool
	internal string
}

func TestSchemaOfFollowsEncodingJSON(t *testing.T) {
	schema, types, err := SchemaOf(reflect.TypeFor[createTask](), "Fallback")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"created_at": "string", "title": "string", "priority": "priority", "due": "string",
		"points": "integer", "weight": "number", "id": "string", "tags": "repeated tag",
		"counts": "map<string, integer>", "extra": "any", "blob": "bytes", "Untagged": "bool",
	}
	if !reflect.DeepEqual(schema.Fields, want) {
		t.Errorf("fields = %v", schema.Fields)
	}
	if schema.Type != "createTask" {
		t.Errorf("type = %q", schema.Type)
	}
	if got := strings.Join(schema.Required, ","); got != "Untagged,counts,created_at,points,tags,title" {
		t.Errorf("required = %s", got)
	}
	if got := strings.Join(schema.Optional, ","); got != "blob,due,extra,id,priority,weight" {
		t.Errorf("optional = %s", got)
	}
	if schema.Descriptions["title"] != "short summary" || !strings.Contains(schema.Descriptions["due"], "RFC 3339") {
		t.Errorf("descriptions = %v", schema.Descriptions)
	}
	if strings.Join(types["priority"].Values, ",") != "low,high" || types["tag"].Fields["name"] != "string" {
		t.Errorf("types = %v", types)
	}
}

type node struct {
	Value    string `json:"value"`
	Children []node `json:"children"`
}

func TestSchemaOfDeclaresARecursiveRoot(t *testing.T) {
	_, types, err := SchemaOf(reflect.TypeFor[node](), "")
	if err != nil {
		t.Fatal(err)
	}
	if types["node"].Fields["children"] != "repeated node" {
		t.Fatalf("types = %v", types)
	}
}

type shadowed struct {
	A string `json:"a"`
}

// encoding/json would silently drop one of the two "a" fields.
type clash struct {
	shadowed
	B string `json:"a"`
}

type opaque struct{}

func (opaque) MarshalJSON() ([]byte, error) { return []byte(`1`), nil }

func TestSchemaOfRefusesShapesItCannotKnow(t *testing.T) {
	for name, value := range map[string]reflect.Type{
		"not a struct": reflect.TypeFor[string](),
		"marshaler":    reflect.TypeFor[struct{ X opaque }](),
		"channel":      reflect.TypeFor[struct{ C chan int }](),
		"bool key":     reflect.TypeFor[struct{ M map[bool]int }](),
		"clash":        reflect.TypeFor[clash](),
	} {
		if _, _, err := SchemaOf(value, "T"); err == nil {
			t.Errorf("%s: described", name)
		}
	}
}

func TestSchemaOfAgreesWithValidate(t *testing.T) {
	schema, types, err := SchemaOf(reflect.TypeFor[createTask](), "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(createTask{Title: "a", Tags: []tag{{Name: "x"}}, Counts: map[string]int{"a": 1}, Priority: "low"})
	if err := ValidateValue(schema, types, encoded); err != nil {
		t.Fatalf("a value of the type fails its own schema: %v\n%s", err, encoded)
	}
}
