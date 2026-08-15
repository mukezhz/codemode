package main

import (
	"encoding/json"
	"testing"

	"github.com/mukezhz/codemode/capability"
	coderegistry "github.com/mukezhz/codemode/registry"

	taskv1 "github.com/mukezhz/codemode/examples/taskreport/gen/task/v1"
	userv1 "github.com/mukezhz/codemode/examples/taskreport/gen/user/v1"
)

// This application derives its registry at startup, but `make generate` also
// writes registry.json from the same contracts. If the two disagreed, a project
// that reviews the JSON in a pull request would be reviewing something other
// than what runs.
func TestDerivedRegistryMatchesTheGeneratedFile(t *testing.T) {
	generated, err := capability.LoadFile("registry.json")
	if err != nil {
		t.Skipf("registry.json is not generated: %v", err)
	}

	tasks, err := coderegistry.FromFiles(taskv1.File_task_proto)
	if err != nil {
		t.Fatal(err)
	}
	users, err := coderegistry.FromFiles(userv1.File_user_proto)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}

	// Compared by name: the two paths visit files in different orders, and
	// order is not part of what a registry means.
	want := byName(t, generated)
	got := byName(t, derived)
	if len(want) != len(got) {
		t.Fatalf("counts differ: registry.json %d, derived %d", len(want), len(got))
	}
	for name, encoded := range want {
		if got[name] != encoded {
			t.Errorf("%s differs:\n  file: %s\n derived: %s", name, encoded, got[name])
		}
	}
}

func byName(t *testing.T, built capability.Registry) map[string]string {
	t.Helper()
	byName := make(map[string]string, len(built.Capabilities))
	for _, item := range built.Capabilities {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		byName[item.Name] = string(encoded)
	}
	return byName
}

// adminOnlyCapability mirrors main.go's list. Duplicated on purpose: if the two
// drift, this test's count is wrong and says so.
var adminOnlyCapability = capability.Named(
	"users.list", "tasks.listFor", "tasks.assign", "reports.byOwner")

// The whole surface this application publishes, in one assertion. A capability
// appearing or vanishing should be a deliberate edit to this list.
func TestPublishedCapabilities(t *testing.T) {
	tasks, _ := coderegistry.FromFiles(taskv1.File_task_proto)
	users, _ := coderegistry.FromFiles(userv1.File_user_proto)
	full, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Capabilities) != 23 {
		t.Fatalf("capabilities = %d, want 23: %v", len(full.Capabilities), full.Names())
	}

	member := full.Select(func(item capability.Capability) bool { return !adminOnlyCapability(item) })
	if len(member.Capabilities) != 19 {
		t.Fatalf("a member sees %d, want 19: %v", len(member.Capabilities), member.Names())
	}
}
