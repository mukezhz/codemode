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
	Planned  bool            `json:"planned,omitempty"`
	// The capability is held for approval; Planned says whether this call was.
	NeedsApproval bool          `json:"needs_approval,omitempty"`
	Duration      time.Duration `json:"duration"`

	err error
}

func (call Call) Err() error { return call.err }

// Safe for concurrent use.
type Recorder struct {
	// Each planned write returns {} to the program.
	Plan bool
	// Approved runs writes that need approval; set only when applying an approved plan.
	Approved bool
	// It must not block.
	OnCall func(Call)

	mutex   sync.Mutex
	calls   []Call
	holding bool
}

// hold reports whether a write is recorded instead of run. Once one write is held
// for approval, every later write is too, so the plan keeps the program's order.
func (recorder *Recorder) hold(item capability.Capability) bool {
	if item.ReadOnly {
		return false
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if item.NeedsApproval && !recorder.Approved {
		recorder.holding = true
	}
	return recorder.Plan || recorder.holding
}

// Pending reports whether a write is waiting for approval.
func (recorder *Recorder) Pending() bool {
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
