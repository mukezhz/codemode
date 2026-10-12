package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrPermanent marks an error a rewritten program cannot fix, such as a permission denial.
var ErrPermanent = errors.New("permanent")

func Permanent(err error) error {
	return fmt.Errorf("%w (%w)", err, ErrPermanent)
}

type Call struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	ReadOnly bool            `json:"read_only,omitempty"`
	Planned  bool            `json:"planned,omitempty"`
	Duration time.Duration   `json:"duration"`

	err error
}

func (call Call) Err() error { return call.err }

// Safe for concurrent use.
type Recorder struct {
	// Each planned write returns {} to the program.
	Plan bool
	// It must not block.
	OnCall func(Call)

	mutex sync.Mutex
	calls []Call
}

func (recorder *Recorder) add(call Call) {
	recorder.mutex.Lock()
	recorder.calls = append(recorder.calls, call)
	recorder.mutex.Unlock()
	if recorder.OnCall != nil {
		recorder.OnCall(call)
	}
}

func (recorder *Recorder) Calls() []Call {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]Call(nil), recorder.calls...)
}

// A failed write may still have committed.
func (recorder *Recorder) Wrote() bool {
	for _, call := range recorder.Calls() {
		if !call.ReadOnly && !call.Planned {
			return true
		}
	}
	return false
}

func (recorder *Recorder) Permanent() error {
	for _, call := range recorder.Calls() {
		if errors.Is(call.err, ErrPermanent) {
			return call.err
		}
	}
	return nil
}

type recorderKey struct{}

func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func recorderFrom(ctx context.Context) *Recorder {
	recorder, _ := ctx.Value(recorderKey{}).(*Recorder)
	return recorder
}
