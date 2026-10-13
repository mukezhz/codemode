// Package wire is the JSON-lines protocol between sandbox/subprocess and
// cmd/codemode-worker. The host treats every line it reads as hostile.
package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const Version = 1

const (
	TypeRun    = "run"
	TypeCall   = "call"
	TypeResult = "result"
	TypeDone   = "done"
	TypeError  = "error"
)

type Limits struct {
	TimeoutMS        int64 `json:"timeout_ms"`
	MaxCalls         int   `json:"max_calls"`
	MaxResponseBytes int   `json:"max_response_bytes"`
	MaxHeapBytes     int   `json:"max_heap_bytes"`
}

type Message struct {
	V      int             `json:"v,omitempty"`
	Type   string          `json:"type"`
	ID     int             `json:"id,omitempty"`
	Engine string          `json:"engine,omitempty"`
	Source string          `json:"source,omitempty"`
	Limits *Limits         `json:"limits,omitempty"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Error  string          `json:"error,omitempty"`
}

var ErrLineTooLong = errors.New("wire: line over the size limit")

type Reader struct {
	scanner *bufio.Scanner
}

func NewReader(r io.Reader, maxLine int) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, min(maxLine, 64<<10)), maxLine)
	return &Reader{scanner: scanner}
}

// Read returns io.EOF when the peer closed cleanly between messages.
func (reader *Reader) Read() (Message, error) {
	if !reader.scanner.Scan() {
		err := reader.scanner.Err()
		if errors.Is(err, bufio.ErrTooLong) {
			return Message{}, ErrLineTooLong
		}
		if err == nil {
			err = io.EOF
		}
		return Message{}, err
	}
	var message Message
	if err := json.Unmarshal(reader.scanner.Bytes(), &message); err != nil {
		return Message{}, fmt.Errorf("wire: %w", err)
	}
	if message.Type == "" {
		return Message{}, errors.New("wire: message has no type")
	}
	return message, nil
}

type Writer struct {
	encoder *json.Encoder
}

func NewWriter(w io.Writer) *Writer { return &Writer{encoder: json.NewEncoder(w)} }

func (writer *Writer) Write(message Message) error { return writer.encoder.Encode(message) }
