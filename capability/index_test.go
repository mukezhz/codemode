package capability

import (
	"strings"
	"testing"
)

func TestEmbeddingTextIncludesContract(t *testing.T) {
	capability := Capability{
		Name:        "students.interviewsWithin",
		RPC:         "student.v1.StudentService/InterviewsWithin",
		Description: "Returns upcoming interviews.",
		Input:       TypeSchema{Type: "Request", Fields: map[string]string{"days": "number"}},
		Output:      TypeSchema{Type: "Response", Fields: map[string]string{"students": "repeated Student"}},
	}
	text := capability.EmbeddingText()
	for _, want := range []string{"students.interviewsWithin", "upcoming interviews", `"days":"number"`, `"students":"repeated Student"`} {
		if !strings.Contains(text, want) {
			t.Errorf("embedding text does not contain %q", want)
		}
	}
}
