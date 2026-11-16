package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Event is one step of a run or a plan. Every event of one run carries its
// RunID; plan events carry the PlanID too, so a log line, a plan and an error
// can be joined on one key.
type Event struct {
	Time   time.Time
	Kind   string
	RunID  string
	PlanID string
	// Stage is where a run failed: cache, search, generate, or run.
	Stage string
	// Name is the capability, on call events.
	Name     string
	Attempt  int
	Count    int
	Duration time.Duration
	Err      string
}

const (
	EventRunStarted     = "run.started"
	EventSearchDone     = "search.done"
	EventProgramWritten = "program.written"
	// A program that failed and goes back to the model for repair.
	EventProgramFailed = "program.failed"
	EventCallDone      = "call.done"
	EventCallHeld      = "call.held"
	EventRunFinished   = "run.finished"
	EventRunFailed     = "run.failed"

	EventPlanHeld     = "plan.held"
	EventPlanApplying = "plan.applying"
	EventPlanApplied  = "plan.applied"
	EventPlanFailed   = "plan.failed"
	EventPlanRejected = "plan.rejected"
	EventPlanExpired  = "plan.expired"
)

// LogEvents writes each event to logger: failures at error level, the rest at info.
func LogEvents(logger *slog.Logger) func(Event) {
	return func(event Event) {
		attributes := []slog.Attr{slog.String("run", event.RunID)}
		add := func(key, value string) {
			if value != "" {
				attributes = append(attributes, slog.String(key, value))
			}
		}
		add("plan", event.PlanID)
		add("stage", event.Stage)
		add("capability", event.Name)
		if event.Attempt > 0 {
			attributes = append(attributes, slog.Int("attempt", event.Attempt))
		}
		if event.Count > 0 {
			attributes = append(attributes, slog.Int("count", event.Count))
		}
		if event.Duration > 0 {
			attributes = append(attributes, slog.Duration("took", event.Duration))
		}
		level := slog.LevelInfo
		if event.Err != "" {
			level = slog.LevelError
			attributes = append(attributes, slog.String("error", event.Err))
		}
		logger.LogAttrs(context.Background(), level, event.Kind, attributes...)
	}
}

// Error is every failure agent.Run returns, except *Pending: which run, and
// at which stage. errors.Is and errors.As see through it to the cause.
type Error struct {
	RunID string
	Stage string
	Err   error
}

func (failure *Error) Error() string {
	return fmt.Sprintf("run %s: %s: %v", failure.RunID, failure.Stage, failure.Err)
}

func (failure *Error) Unwrap() error { return failure.Err }

const (
	StageCache    = "cache"
	StageSearch   = "search"
	StageGenerate = "generate"
	StageRun      = "run"
)
