package capability

import (
	"encoding/json"
	"slices"
	"strings"
)

type Registry struct {
	Capabilities []Capability `json:"capabilities"`
}

type Capability struct {
	Name        string     `json:"name"`
	RPC         string     `json:"rpc"`
	Description string     `json:"description"`
	Input       TypeSchema `json:"input"`
	Output      TypeSchema `json:"output"`

	Types map[string]TypeSchema `json:"types,omitempty"`

	// A caller holding any of Roles or any of Permissions may call it; both
	// empty means unrestricted.
	Roles       []string `json:"roles,omitempty"`
	Permissions []string `json:"permissions,omitempty"`

	// Anything but (idempotency_level) = NO_SIDE_EFFECTS is treated as a write.
	ReadOnly bool `json:"read_only,omitempty"`
}

func (capability Capability) Unrestricted() bool {
	return len(capability.Roles) == 0 && len(capability.Permissions) == 0
}

func (capability Capability) AllowsRole(role string) bool {
	return capability.Unrestricted() || slices.Contains(capability.Roles, role)
}

// Allows reports whether caller holds any role or permission this capability accepts.
func (capability Capability) Allows(caller Caller) bool {
	if capability.Unrestricted() {
		return true
	}
	for _, role := range caller.Roles {
		if slices.Contains(capability.Roles, strings.TrimSpace(role)) {
			return true
		}
	}
	for _, permission := range caller.Permissions {
		if slices.Contains(capability.Permissions, strings.TrimSpace(permission)) {
			return true
		}
	}
	return false
}

// Caller is what your application says the signed-in user holds, fetched per
// request: fixed roles from the session, and permissions from wherever runtime
// roles are defined. Either may be empty.
type Caller struct {
	Roles       []string
	Permissions []string
}

func (capability *Capability) EmbeddingText() string {
	input, _ := json.Marshal(capability.Input)
	output, _ := json.Marshal(capability.Output)
	// Types are excluded: they are shared, so embedding them would pull unrelated contracts together.
	return strings.Join([]string{
		capability.Name,
		capability.Description,
		capability.RPC,
		"input " + string(input),
		"output " + string(output),
	}, "\n")
}

type TypeSchema struct {
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields"`
}
