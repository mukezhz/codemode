package capability

import (
	"encoding/json"
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
}

// EmbeddingText returns the contract content used for semantic indexing.
func (capability *Capability) EmbeddingText() string {
	input, _ := json.Marshal(capability.Input)
	output, _ := json.Marshal(capability.Output)
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
