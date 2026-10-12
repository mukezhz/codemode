package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrPermanent marks a capability error that a rewritten program cannot fix,
// such as a permission denial; the agent stops instead of retrying.
var ErrPermanent = errors.New("permanent")

// Permanent wraps err so errors.Is(err, ErrPermanent) reports true.
func Permanent(err error) error {
	return fmt.Errorf("%w (%w)", err, ErrPermanent)
}

// Call is one capability invocation made by a program.
type Call struct {
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	ReadOnly bool            `json:"read_only,omitempty"`
	// Planned marks a write recorded in plan mode but not executed.
	Planned  bool          `json:"planned,omitempty"`
	Duration time.Duration `json:"duration"`

	err error
}

// Err returns the error the call failed with, nil on success.
func (call Call) Err() error { return call.err }

// Recorder collects the calls made under a context from WithRecorder. It is
// safe for concurrent use.
type Recorder struct {
	// Plan records writes without executing them; each returns {} to the program.
	Plan bool

	mutex sync.Mutex
	calls []Call
}

func (recorder *Recorder) add(call Call) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.calls = append(recorder.calls, call)
}

// Calls returns the recorded calls in order.
func (recorder *Recorder) Calls() []Call {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]Call(nil), recorder.calls...)
}

// Wrote reports whether a write was attempted; a failed write may still have committed.
func (recorder *Recorder) Wrote() bool {
	for _, call := range recorder.Calls() {
		if !call.ReadOnly && !call.Planned {
			return true
		}
	}
	return false
}

// Permanent returns the first recorded error marked with ErrPermanent.
func (recorder *Recorder) Permanent() error {
	for _, call := range recorder.Calls() {
		if errors.Is(call.err, ErrPermanent) {
			return call.err
		}
	}
	return nil
}

type recorderKey struct{}

// WithRecorder returns a context whose Execute calls are recorded by recorder.
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func recorderFrom(ctx context.Context) *Recorder {
	recorder, _ := ctx.Value(recorderKey{}).(*Recorder)
	return recorder
}
