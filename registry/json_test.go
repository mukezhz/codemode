package registry

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/wrapperspb"

	fixturev1 "github.com/mukezhz/codemode/registry/internal/fixturepb/gen/fixture/v1"
)

func TestMarshalJSONMatchesTheSchemaTheModelSees(t *testing.T) {
	response := &fixturev1.ListOrdersResponse{Orders: []*fixturev1.Order{{Id: 7, Reference: "A-7"}}}
	encoded, err := MarshalJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	order := decoded.Orders[0]
	if order["id"] != float64(7) {
		t.Errorf("id = %#v, want the number 7, not a string", order["id"])
	}
	if items, ok := order["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %#v, want [] so a program can take its length", order["items"])
	}
	if order["shipped"] != false {
		t.Errorf("shipped = %#v, want false present", order["shipped"])
	}

	wrapped, err := MarshalJSON(&fixturev1.Order{ParentId: wrapperspb.Int64(9)})
	if err != nil {
		t.Fatal(err)
	}
	var parent struct {
		ParentID any `json:"parent_id"`
	}
	if err := json.Unmarshal(wrapped, &parent); err != nil || parent.ParentID != float64(9) {
		t.Errorf("parent_id = %#v, want the number 9 the schema promises", parent.ParentID)
	}

	empty, err := MarshalJSON(&fixturev1.ListOrdersResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `{"orders":[]}` {
		t.Errorf("empty = %s, want {\"orders\":[]}", empty)
	}

	var roundTrip fixturev1.ListOrdersResponse
	if err := protojson.Unmarshal(encoded, &roundTrip); err != nil || roundTrip.Orders[0].GetId() != 7 {
		t.Errorf("protojson.Unmarshal cannot read it back: %v", err)
	}
}
