package capability

import (
	"fmt"
	"sort"
	"strings"
)

// Merge combines registries, rejecting a capability name that appears twice.
//
// A large application assembles its surface from parts: one team owns contacts,
// another owns deals, a third owns billing. Each builds its own Registry — from
// its own proto files, or loaded from its own JSON — and the application joins
// them here:
//
//	full, err := capability.Merge(crm.Capabilities(), billing.Capabilities())
//
// A duplicate name is an error rather than a silent last-one-wins, because the
// name is what a model calls and what Execute resolves. Two teams both claiming
// "contacts.list" is a namespace collision to settle in the protos, not
// something to discover in production.
func Merge(registries ...Registry) (Registry, error) {
	merged := Registry{Capabilities: []Capability{}}
	owner := map[string]int{}

	for index, registry := range registries {
		for _, item := range registry.Capabilities {
			if previous, exists := owner[item.Name]; exists {
				return Registry{}, fmt.Errorf(
					"capability %q is defined in registry %d and again in registry %d",
					item.Name, previous, index)
			}
			owner[item.Name] = index
			merged.Capabilities = append(merged.Capabilities, item)
		}
	}
	return merged, nil
}

// Select returns the capabilities matching keep, in their original order.
//
// This is how a capability surface is narrowed per caller. A registry is just
// data, and building a second one is cheap, so a request from someone who may
// not delete anything gets a runtime that has no delete capability at all:
//
//	allowed := full.Select(func(item capability.Capability) bool {
//		return identity.Admin || !strings.HasSuffix(item.Name, ".delete")
//	})
//	mode, err := codemode.New(allowed, search, execute)
//
// Narrowing the registry removes the capability from what may run, not merely
// from what the model is shown. It does not replace checking permissions inside
// ExecuteFunc: the registry knows which capability is being called, never which
// rows the caller may see.
func (registry Registry) Select(keep func(Capability) bool) Registry {
	selected := Registry{Capabilities: make([]Capability, 0, len(registry.Capabilities))}
	for _, item := range registry.Capabilities {
		if keep(item) {
			selected.Capabilities = append(selected.Capabilities, item)
		}
	}
	return selected
}

// Namespaces returns each namespace once, sorted. A namespace is the part of a
// capability name before the dot, which is the service's
// (codemode.namespace) option.
func (registry Registry) Namespaces() []string {
	unique := map[string]bool{}
	for _, item := range registry.Capabilities {
		if namespace, _, found := strings.Cut(item.Name, "."); found {
			unique[namespace] = true
		}
	}
	namespaces := make([]string, 0, len(unique))
	for namespace := range unique {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

// InNamespace returns a predicate for Select matching any of namespaces. It
// covers the common case of scoping by subsystem:
//
//	readOnly := full.Select(capability.InNamespace("reports", "contacts"))
func InNamespace(namespaces ...string) func(Capability) bool {
	wanted := make(map[string]bool, len(namespaces))
	for _, namespace := range namespaces {
		wanted[namespace] = true
	}
	return func(item Capability) bool {
		namespace, _, _ := strings.Cut(item.Name, ".")
		return wanted[namespace]
	}
}

// Named returns a predicate for Select matching exactly these capability names.
func Named(names ...string) func(Capability) bool {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	return func(item Capability) bool { return wanted[item.Name] }
}

// Names returns every capability name, in registry order.
func (registry Registry) Names() []string {
	names := make([]string, len(registry.Capabilities))
	for index, item := range registry.Capabilities {
		names[index] = item.Name
	}
	return names
}
