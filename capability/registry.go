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

// ForRoles keeps the capabilities any of roles allows, plus every unrestricted
// one. Pass whatever the caller holds, fetched per request: role names, or the
// permission names your protos declare. No roles still gets the unrestricted
// capabilities; blank and unknown names grant nothing.
func (registry Registry) ForRoles(roles ...string) Registry {
	held := map[string]bool{}
	for _, role := range roles {
		if role = strings.TrimSpace(role); role != "" {
			held[role] = true
		}
	}
	return registry.Select(func(item Capability) bool {
		if len(item.Roles) == 0 {
			return true
		}
		for _, role := range item.Roles {
			if held[role] {
				return true
			}
		}
		return false
	})
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
	return registry.Select(func(item Capability) bool { return len(item.Roles) == 0 })
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
