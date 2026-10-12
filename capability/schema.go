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

	// Types are the message shapes Input and Output refer to transitively, keyed by unqualified name.
	Types map[string]TypeSchema `json:"types,omitempty"`

	// Roles are the caller roles allowed to use this capability; empty means unrestricted.
	Roles []string `json:"roles,omitempty"`

	// ReadOnly is set by (idempotency_level) = NO_SIDE_EFFECTS; anything else is treated as a write.
	ReadOnly bool `json:"read_only,omitempty"`
}

// AllowsRole reports whether role may use this capability. A capability with no
// roles is unrestricted.
func (capability Capability) AllowsRole(role string) bool {
	if len(capability.Roles) == 0 {
		return true
	}
	return slices.Contains(capability.Roles, role)
}

// EmbeddingText returns the contract content used for semantic indexing.
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
