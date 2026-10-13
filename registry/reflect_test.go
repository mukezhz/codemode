package registry_test

import (
	"strings"
	"testing"

	"github.com/mukezhz/codemode/registry"

	// Both fixture files are linked so FromGlobalFiles sees more than FromFiles.
	fixturev1 "github.com/mukezhz/codemode/registry/internal/fixturepb/gen/fixture/v1"
	shippingv1 "github.com/mukezhz/codemode/registry/internal/fixturepb/gen/shipping/v1"
)

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

	pair, err := registry.FromFiles(fixturev1.File_orders_proto, shippingv1.File_shipping_proto)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(pair.Namespaces(), ","); got != "orders,shipping" {
		t.Fatalf("namespaces = %q, want both", got)
	}
}

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
	if len(built.Capabilities) != 4 {
		t.Fatalf("capabilities = %d, want 4: %v", len(built.Capabilities), built.Names())
	}
}

func TestFromFilesRejectsNoInput(t *testing.T) {
	if _, err := registry.FromFiles(); err == nil {
		t.Fatal("FromFiles accepted no descriptors")
	}
}

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
		// Input and Output types are excluded from Types.
		if _, repeated := item.Types["ListOrdersResponse"]; repeated {
			t.Error("the response type is repeated in Types")
		}
		return
	}
	t.Fatal("orders.list is missing")
}

func TestRolesAndPermissionsComeFromTheContract(t *testing.T) {
	built, err := registry.FromFiles(fixturev1.File_orders_proto)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]registry.Capability{}
	for _, item := range built.Capabilities {
		byName[item.Name] = item
	}
	if list := byName["orders.list"]; len(list.Roles) != 0 || strings.Join(list.Permissions, ",") != "orders.read" {
		t.Errorf("orders.list roles=%v permissions=%v, want the service default permission", list.Roles, list.Permissions)
	}
	cancel := byName["orders.cancel"]
	if strings.Join(cancel.Roles, ",") != "support" || strings.Join(cancel.Permissions, ",") != "orders.cancel" {
		t.Errorf("orders.cancel roles=%v permissions=%v, want its own, replacing the default", cancel.Roles, cancel.Permissions)
	}
}
