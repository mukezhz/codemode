package capability

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Rejects a capability name that appears twice.
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

// Does not replace row-level checks in ExecuteFunc.
func (registry Registry) ForRole(role string) Registry {
	return registry.Select(func(item Capability) bool { return item.AllowsRole(role) })
}

// For keeps the capabilities caller may call: those whose roles or permissions
// caller holds any of, plus every unrestricted one. Build caller per request
// from your application; unknown, blank and wrongly cased names grant nothing.
func (registry Registry) For(caller Caller) Registry {
	return registry.Select(func(item Capability) bool { return item.Allows(caller) })
}

// Only ever narrows: a capability with no roles now requires role.
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

func (registry Registry) Unrestricted() Registry {
	return registry.Select(func(item Capability) bool { return item.Unrestricted() })
}

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

// Permissions lists every permission the registry's capabilities accept: the
// checkboxes an admin screen offers when someone defines a role at runtime.
func (registry Registry) Permissions() []string {
	unique := map[string]bool{}
	for _, item := range registry.Capabilities {
		for _, permission := range item.Permissions {
			unique[permission] = true
		}
	}
	permissions := make([]string, 0, len(unique))
	for permission := range unique {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions
}

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

func Named(names ...string) func(Capability) bool {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	return func(item Capability) bool { return wanted[item.Name] }
}

func (registry Registry) Names() []string {
	names := make([]string, len(registry.Capabilities))
	for index, item := range registry.Capabilities {
		names[index] = item.Name
	}
	return names
}
