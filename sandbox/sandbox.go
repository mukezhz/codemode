package sandbox

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mukezhz/code-mode/capability"
)

// Declarations renders matches as TypeScript signatures for a model prompt.
// Only these capabilities are described to the model; the executable allowlist
// remains the full registry held by the code mode runtime.
func Declarations(matches []capability.Match) string {
	var builder strings.Builder
	for _, match := range matches {
		item := match.Capability
		fmt.Fprintf(&builder, "/** %s */\ncodemode.execute(name: %q, input: %s): %s;\n\n",
			item.Description, item.Name, object(item.Input), object(item.Output))
	}
	return builder.String()
}

func object(schema capability.TypeSchema) string {
	if len(schema.Fields) == 0 {
		return "{}"
	}
	fields := make([]string, 0, len(schema.Fields))
	for name, fieldType := range schema.Fields {
		fields = append(fields, name+": "+fieldType)
	}
	sort.Strings(fields) // A stable prompt keeps model responses cacheable.
	return "{ " + strings.Join(fields, "; ") + " }"
}
