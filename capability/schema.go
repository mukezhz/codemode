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

	// Types are the message shapes Input and Output refer to, transitively.
	//
	// Without these a model is told that a call returns "repeated GroupCount"
	// and nothing about what a GroupCount holds, so it guesses field names —
	// and a program that reads group.project when the field is group.name
	// produces "undefined" rather than an error. The guess is invisible until
	// someone reads the answer.
	//
	// Keyed by unqualified type name, which is how the field strings refer to
	// them.
	Types map[string]TypeSchema `json:"types,omitempty"`
}

// EmbeddingText returns the contract content used for semantic indexing.
func (capability *Capability) EmbeddingText() string {
	input, _ := json.Marshal(capability.Input)
	output, _ := json.Marshal(capability.Output)
	// Types are deliberately excluded: they are shared between capabilities, so
	// embedding them would pull unrelated contracts towards each other.
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
