package wire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	writer := NewWriter(&buffer)
	sent := []Message{
		{V: Version, Type: TypeRun, Engine: "goja", Source: "return 1", Limits: &Limits{MaxCalls: 3}},
		{Type: TypeCall, ID: 1, Name: "todos.list", Input: []byte(`{}`)},
		{Type: TypeResult, ID: 1, Error: "nope"},
		{Type: TypeDone, Value: []byte(`"ok"`)},
	}
	for _, message := range sent {
		if err := writer.Write(message); err != nil {
			t.Fatal(err)
		}
	}

	reader := NewReader(&buffer, 1024)
	for _, want := range sent {
		got, err := reader.Read()
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != want.Type || got.ID != want.ID || got.Name != want.Name ||
			got.Error != want.Error || string(got.Value) != string(want.Value) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	if _, err := reader.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want EOF", err)
	}
}

func TestReadRejectsHostileLines(t *testing.T) {
	for name, input := range map[string]string{
		"oversized": `{"type":"done","value":"` + strings.Repeat("a", 100) + `"}` + "\n",
		"not json":  "garbage\n",
		"untyped":   `{"id":1}` + "\n",
		"truncated": `{"type":"do`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewReader(strings.NewReader(input), 64).Read()
			if err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("err = %v, want a protocol error", err)
			}
		})
	}
}

func FuzzRead(f *testing.F) {
	f.Add(`{"type":"call","id":1,"name":"a.b","input":{}}` + "\n")
	f.Add(`{"type":"done","value":null}` + "\n")
	f.Add("garbage\n")
	f.Fuzz(func(t *testing.T, input string) {
		reader := NewReader(strings.NewReader(input), 256)
		for range 8 {
			if _, err := reader.Read(); err != nil {
				return
			}
		}
	})
}
