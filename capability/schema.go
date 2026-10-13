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

	// Empty means unrestricted.
	Roles []string `json:"roles,omitempty"`

	// Anything but (idempotency_level) = NO_SIDE_EFFECTS is treated as a write.
	ReadOnly bool `json:"read_only,omitempty"`
}

func (capability Capability) AllowsRole(role string) bool {
	if len(capability.Roles) == 0 {
		return true
	}
	return slices.Contains(capability.Roles, role)
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
