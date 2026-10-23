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

	// Any match allows; both empty means unrestricted.
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
	for _, held := range caller.Permissions {
		for _, required := range capability.Permissions {
			if Grants(strings.TrimSpace(held), required) {
				return true
			}
		}
	}
	return false
}

// Fetched per request; either may be empty.
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
	Type         string              `json:"type"`
	Fields       map[string]string   `json:"fields"`
	Descriptions map[string]string   `json:"descriptions,omitempty"`
	Optional     []string            `json:"optional,omitempty"`
	Oneofs       map[string][]string `json:"oneofs,omitempty"`
	// Set only on an enum: the names it accepts and returns.
	Values []string `json:"values,omitempty"`
}
