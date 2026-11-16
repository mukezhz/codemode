package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mukezhz/codemode/capability"
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
	// Held is a write recorded instead of run; it waits in a plan.
	Held bool `json:"held,omitempty"`
	// The capability needs approval; Held says whether this call was held.
	NeedsApproval bool          `json:"needs_approval,omitempty"`
	Duration      time.Duration `json:"duration"`

	err error
}

func (call Call) Err() error { return call.err }

// Safe for concurrent use.
type Recorder struct {
	// HoldWrites holds every write, not only those that need approval. A held
	// write returns {} to the program.
	HoldWrites bool
	// It must not block.
	OnCall func(Call)

	approved bool
	mutex    sync.Mutex
	calls    []Call
	holding  bool
}

// WithApproval runs every write, including those that need approval. Use it
// when a person has already said yes, such as a delete button they pressed.
func WithApproval(ctx context.Context) context.Context {
	return WithRecorder(ctx, &Recorder{approved: true})
}

// hold reports whether a write is recorded instead of run. Once one write is held
// for approval, every later write is too, so the plan keeps the program's order.
func (recorder *Recorder) hold(item capability.Capability) bool {
	if item.ReadOnly {
		return false
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if (item.NeedsApproval && !recorder.approved) || recorder.HoldWrites {
		recorder.holding = true
	}
	return recorder.holding
}

// Held reports whether any write was held instead of run.
func (recorder *Recorder) Held() bool {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.holding
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
		if !call.ReadOnly && !call.Held {
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
