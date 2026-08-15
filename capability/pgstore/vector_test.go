package pgstore

import "testing"

func TestFormatVectorMatchesPgvectorText(t *testing.T) {
	if got := formatVector([]float32{1, -0.5, 0}); got != "[1,-0.5,0]" {
		t.Fatalf("formatVector() = %q, want %q", got, "[1,-0.5,0]")
	}
	if got := formatVector(nil); got != "[]" {
		t.Fatalf("formatVector(nil) = %q, want %q", got, "[]")
	}
}
