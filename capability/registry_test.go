package capability_test

import (
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func registryOf(names ...string) capability.Registry {
	built := capability.Registry{}
	for _, name := range names {
		built.Capabilities = append(built.Capabilities, capability.Capability{
			Name: name, RPC: "svc/" + name, Description: name,
		})
	}
	return built
}

func TestMergeCombinesSubsystems(t *testing.T) {
	merged, err := capability.Merge(
		registryOf("contacts.list", "contacts.create"),
		registryOf("deals.list"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(merged.Names(), ","); got != "contacts.list,contacts.create,deals.list" {
		t.Fatalf("names = %q, want registry order preserved", got)
	}
}

// Last-one-wins would let one team silently take over another's capability
// name, and the failure would show up as a model calling the wrong code.
func TestMergeRejectsADuplicateName(t *testing.T) {
	_, err := capability.Merge(registryOf("contacts.list"), registryOf("contacts.list"))
	if err == nil {
		t.Fatal("Merge accepted a duplicate capability name")
	}
	if !strings.Contains(err.Error(), "contacts.list") {
		t.Fatalf("error = %v, want it to name the collision", err)
	}
}

func TestSelectNarrowsTheSurface(t *testing.T) {
	full := registryOf("tasks.list", "tasks.delete", "reports.summary")

	readOnly := full.Select(func(item capability.Capability) bool {
		return !strings.HasSuffix(item.Name, ".delete")
	})
	if got := strings.Join(readOnly.Names(), ","); got != "tasks.list,reports.summary" {
		t.Fatalf("names = %q", got)
	}
	// Select returns a new registry; narrowing per request must not mutate the
	// one the application built at startup.
	if len(full.Capabilities) != 3 {
		t.Fatal("Select mutated the source registry")
	}
}

func TestNamespaceAndNamePredicates(t *testing.T) {
	full := registryOf("tasks.list", "tasks.delete", "reports.summary", "users.me")

	if got := strings.Join(full.Select(capability.InNamespace("reports", "users")).Names(), ","); got != "reports.summary,users.me" {
		t.Fatalf("InNamespace = %q", got)
	}
	if got := strings.Join(full.Select(capability.Named("tasks.list")).Names(), ","); got != "tasks.list" {
		t.Fatalf("Named = %q", got)
	}
	if got := strings.Join(full.Namespaces(), ","); got != "reports,tasks,users" {
		t.Fatalf("Namespaces = %q, want them sorted and deduplicated", got)
	}
}
