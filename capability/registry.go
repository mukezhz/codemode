package capability

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Merge combines registries, rejecting a capability name that appears twice.
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
// Narrowing removes a capability from what may run, not only from what the model sees.
func (registry Registry) Select(keep func(Capability) bool) Registry {
	selected := Registry{Capabilities: make([]Capability, 0, len(registry.Capabilities))}
	for _, item := range registry.Capabilities {
		if keep(item) {
			selected.Capabilities = append(selected.Capabilities, item)
		}
	}
	return selected
}

// ForRole returns the capabilities role may use; it does not replace row-level checks in ExecuteFunc.
func (registry Registry) ForRole(role string) Registry {
	return registry.Select(func(item Capability) bool { return item.AllowsRole(role) })
}

// Require returns a copy in which every capability matching keep also accepts role.
// It only ever narrows: a capability with no roles now requires role.
func (registry Registry) Require(role string, keep func(Capability) bool) Registry {
	role = strings.TrimSpace(role)
	if role == "" {
		return registry
	}
	required := Registry{Capabilities: make([]Capability, 0, len(registry.Capabilities))}
	for _, item := range registry.Capabilities {
		if keep(item) && !slices.Contains(item.Roles, role) {
			// Copy so derived registries never share backing storage.
			item.Roles = append(append([]string{}, item.Roles...), role)
			sort.Strings(item.Roles)
		}
		required.Capabilities = append(required.Capabilities, item)
	}
	return required
}

// Unrestricted returns the capabilities that declare no roles.
func (registry Registry) Unrestricted() Registry {
	return registry.Select(func(item Capability) bool { return len(item.Roles) == 0 })
}

// Roles returns every role named by any capability, sorted.
func (registry Registry) Roles() []string {
	unique := map[string]bool{}
	for _, item := range registry.Capabilities {
		for _, role := range item.Roles {
			unique[role] = true
		}
	}
	roles := make([]string, 0, len(unique))
	for role := range unique {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// Namespaces returns each namespace (the name part before the dot) once, sorted.
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

// InNamespace returns a predicate for Select matching any of namespaces.
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
