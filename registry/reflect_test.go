package registry_test

import (
	"strings"
	"testing"

	"github.com/mukezhz/codemode/registry"

	// The registry's own fixture, so a library test does not reach into an
	// example. Both files are linked, which is what lets FromGlobalFiles see
	// more than FromFiles is handed.
	fixturev1 "github.com/mukezhz/codemode/registry/internal/fixturepb/gen/fixture/v1"
	shippingv1 "github.com/mukezhz/codemode/registry/internal/fixturepb/gen/shipping/v1"
)

// FromFiles is the short path: generated code already carries its descriptors,
// so a consumer needs no descriptor set, no JSON, and no build step.
func TestFromFilesReadsLinkedDescriptors(t *testing.T) {
	built, err := registry.FromFiles(fixturev1.File_orders_proto)
	if err != nil {
		t.Fatalf("FromFiles returned error: %v", err)
	}

	byName := map[string]string{}
	for _, item := range built.Capabilities {
		byName[item.Name] = item.RPC
	}
	for name, rpc := range map[string]string{
		"orders.list":   "fixture.v1.OrderService/List",
		"orders.get":    "fixture.v1.OrderService/Get",
		"orders.cancel": "fixture.v1.OrderService/Cancel",
	} {
		if byName[name] != rpc {
			t.Errorf("%s = %q, want %q", name, byName[name], rpc)
		}
	}
	if len(built.Capabilities) != 3 {
		t.Fatalf("capabilities = %d, want 3", len(built.Capabilities))
	}
}

// The descriptions and schemas are the whole point — search matches on one and
// the model reads the other. The fixture carries one field of every shape the
// neutral type renderer handles.
func TestFromFilesCarriesDescriptionsAndSchemas(t *testing.T) {
	built, err := registry.FromFiles(fixturev1.File_orders_proto)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range built.Capabilities {
		if item.Description == "" {
			t.Errorf("%s has no description", item.Name)
		}
		if item.Name == "orders.list" {
			if got := item.Output.Fields["orders"]; got != "repeated Order" {
				t.Errorf("output = %q, want %q", got, "repeated Order")
			}
		}
		if item.Name == "orders.get" {
			if got := item.Input.Fields["id"]; got != "number" {
				t.Errorf("input id = %q, want %q", got, "number")
			}
		}
	}
}

// FromFiles scans only what it is handed, which is what makes it the safer
// habit: a new transitive dependency cannot widen the registry.
func TestFromFilesScansOnlyTheFilesGiven(t *testing.T) {
	built, err := registry.FromFiles(fixturev1.File_orders_proto)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range built.Capabilities {
		if !strings.HasPrefix(item.Name, "orders.") {
			t.Fatalf("FromFiles returned %q from a file it was not given", item.Name)
		}
	}

	// Handed both, it returns both.
	pair, err := registry.FromFiles(fixturev1.File_orders_proto, shippingv1.File_shipping_proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pair.Namespaces(), ","); got != "orders,shipping" {
		t.Fatalf("namespaces = %q, want both", got)
	}
}

// Every linked file is scanned, but only annotated services contribute — so
// linking an unrelated protobuf package cannot widen the registry.
func TestFromGlobalFilesFindsEveryAnnotatedService(t *testing.T) {
	built, err := registry.FromGlobalFiles()
	if err != nil {
		t.Fatalf("FromGlobalFiles returned error: %v", err)
	}
	for _, want := range []string{"orders", "shipping"} {
		if !strings.Contains(strings.Join(built.Namespaces(), ","), want) {
			t.Errorf("namespace %q is missing from %v", want, built.Namespaces())
		}
	}
	// codemode.proto is linked too and declares no service, so it must
	// contribute nothing rather than error.
	if len(built.Capabilities) != 4 {
		t.Fatalf("capabilities = %d, want 4: %v", len(built.Capabilities), built.Names())
	}
}

func TestFromFilesRejectsNoInput(t *testing.T) {
	if _, err := registry.FromFiles(); err == nil {
		t.Fatal("FromFiles accepted no descriptors")
	}
}

// The same binary must produce the same registry every run, or the prompt built
// from it stops being cacheable. RangeFiles walks a map, so this is a real risk.
func TestFromGlobalFilesIsDeterministic(t *testing.T) {
	first, err := registry.FromGlobalFiles()
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		next, err := registry.FromGlobalFiles()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(first.Names(), ",") != strings.Join(next.Names(), ",") {
			t.Fatalf("order changed: %v then %v", first.Names(), next.Names())
		}
	}
}

// A model told only that a call returns "repeated Order" has to guess what an
// Order holds, and a wrong guess reads as undefined rather than failing. The
// referenced shapes travel with the capability so it never has to guess.
func TestFromFilesCarriesReferencedShapes(t *testing.T) {
	built, err := registry.FromFiles(fixturev1.File_orders_proto)
	if err != nil {
		t.Fatal(err)
	}

	for _, item := range built.Capabilities {
		if item.Name != "orders.list" {
			continue
		}
		order, found := item.Types["Order"]
		if !found {
			t.Fatalf("orders.list carries no Order shape: %v", item.Types)
		}
		for field, want := range map[string]string{
			"id":        "number",
			"reference": "string",
			"shipped":   "bool",
			"items":     "repeated string",
			"labels":    "map<string, string>",
			"receipt":   "bytes",
		} {
			if order.Fields[field] != want {
				t.Errorf("Order.%s = %q, want %q", field, order.Fields[field], want)
			}
		}
		// The request and response themselves are already spelled out by Input
		// and Output; repeating them would only pad the prompt.
		if _, repeated := item.Types["ListOrdersResponse"]; repeated {
			t.Error("the response type is repeated in Types")
		}
		return
	}
	t.Fatal("orders.list is missing")
}
