// Package agent searches capabilities, has a model write a program composing them, and runs it in the sandbox.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/program"
	"github.com/mukezhz/codemode/sandbox"
)

type Options struct {
	Limit    int
	Attempts int
	// Required; the caller closes it.
	Runner       sandbox.Runner
	Instructions string
	// nil disables caching; the caller closes it.
	Cache program.Cache
	// Separates cache entries that must not be shared, such as a role or tenant.
	CacheCaller string
	// Includes repair attempts; nil disables streaming. It must not block.
	OnChunk func(string)
	// Plan records writes instead of executing them; review Result.Planned, then pass it to Apply.
	// ponytail: a planned write returns {} to the program, so logic that reads a write's output plans wrongly.
	Plan bool
	// nil disables it. It must not block.
	OnCall func(codemode.Call)
	// Stops repair attempts once spent; zero disables. Only providers that report usage count.
	MaxTokens int64
	// Plans keeps writes held for approval until someone decides; nil returns them in Pending only.
	Plans *Plans
	// PlanOwner is who may decide a held plan, such as a user ID.
	PlanOwner string
	// OnEvent receives every step of the run, each carrying Result.RunID. It must not block.
	OnEvent func(Event)

	// Set by RunAs: checks the returned value, so a mismatch is repaired like a failure.
	check func(any) error
	runID string
}

func (options Options) emit(event Event) {
	if options.OnEvent != nil {
		event.Time = time.Now()
		event.RunID = options.runID
		options.OnEvent(event)
	}
}

const (
	DefaultLimit    = 5
	DefaultAttempts = 3
)

func (options Options) checked() (Options, error) {
	if options.Limit < 0 || options.Attempts < 0 {
		return options, errors.New("limit and attempts must not be negative")
	}
	if options.Runner == nil {
		return options, errors.New("Options.Runner is required: build one with an engine package, or with sandbox.NewFromEnv")
	}
	if options.Limit == 0 {
		options.Limit = DefaultLimit
	}
	if options.Attempts == 0 {
		options.Attempts = DefaultAttempts
	}
	return options, nil
}

type Result struct {
	// RunID is on every event and error of this run, and on the plan it held.
	RunID  string
	Answer any
	Source string
	// Empty on a cache hit.
	Matches []capability.Match
	// Zero on a cache hit.
	Attempts int
	Cached   bool
	Timing   Timing
	Calls    []codemode.Call
	Planned  []codemode.Call
	Usage    llm.Usage
}

var ErrTokenBudget = errors.New("token budget exhausted")

// A retry could repeat a write or cannot fix the failure.
var ErrNotRetried = errors.New("not retried")

type Timing struct {
	Total    time.Duration
	Cache    time.Duration
	Search   time.Duration
	Generate time.Duration
	// Run sums every sandbox run, including stale cached programs and failed attempts.
	Run time.Duration
}

func (timing Timing) String() string {
	return fmt.Sprintf("total=%s cache=%s search=%s generate=%s run=%s",
		timing.Total.Round(time.Microsecond), timing.Cache.Round(time.Microsecond),
		timing.Search.Round(time.Microsecond), timing.Generate.Round(time.Microsecond),
		timing.Run.Round(time.Microsecond))
}

func Run(ctx context.Context, mode codemode.CodeMode, model llm.Client, request string, options Options) (Result, error) {
	started := time.Now()
	if mode == nil || model == nil {
		return Result{}, errors.New("code mode runtime and model are required")
	}
	if strings.TrimSpace(request) == "" {
		return Result{}, errors.New("request is required")
	}
	options, err := options.checked()
	if err != nil {
		return Result{}, err
	}
	options.runID = newID(8)
	options.emit(Event{Kind: EventRunStarted})

	runner := options.Runner
	var timing Timing
	meter := &llm.Meter{}
	ctx = llm.WithMeter(ctx, meter)
	stage := StageCache
	finish := func(result Result, err error) (Result, error) {
		timing.Total = time.Since(started)
		result.RunID = options.runID
		result.Timing = timing
		result.Usage = meter.Usage()
		var pending *Pending
		switch {
		case errors.As(err, &pending):
			options.emit(Event{Kind: EventRunFinished, PlanID: pending.ID, Count: len(pending.Calls), Duration: timing.Total})
			return result, err
		case err != nil:
			options.emit(Event{Kind: EventRunFailed, Stage: stage, Duration: timing.Total, Err: err.Error()})
			return result, &Error{RunID: options.runID, Stage: stage, Err: err}
		}
		options.emit(Event{Kind: EventRunFinished, Duration: timing.Total})
		return result, nil
	}

	key, caching := cacheKey(mode, runner, request, options)
	if caching {
		if result, hit, err := fromCache(ctx, mode, key, options, &timing); hit || err != nil {
			stage = StageRun
			return finish(result, err)
		}
	}

	stage = StageSearch
	search := time.Now()
	matches, err := mode.Search(ctx, request, options.Limit)
	timing.Search = time.Since(search)
	if err != nil {
		return finish(Result{}, err)
	}
	options.emit(Event{Kind: EventSearchDone, Count: len(matches), Duration: timing.Search})
	if len(matches) == 0 {
		return finish(Result{}, errors.New("search matched no capabilities; check the capability descriptions, or the caller's roles"))
	}

	result, err := generate(ctx, mode, model, request, matches, options, &timing, &stage)
	if err != nil {
		return finish(result, err)
	}
	if caching && reusable(result) {
		// A failed cache write must not fail an answered request.
		_ = options.Cache.Put(ctx, key, result.Source)
	}
	return finish(result, nil)
}

// Sandbox errors are the model's to fix; transport errors are not.
func generate(ctx context.Context, mode codemode.CodeMode, model llm.Client, request string, matches []capability.Match, options Options, timing *Timing, stage *string) (Result, error) {
	system := options.Runner.Prompt(matches)
	if instructions := strings.TrimSpace(options.Instructions); instructions != "" {
		system += "\nAdditional instructions:\n" + instructions + "\n"
	}
	prompt := request
	result := Result{Matches: matches}

	var lastErr error
	for attempt := 1; attempt <= options.Attempts; attempt++ {
		result.Attempts = attempt

		*stage = StageGenerate
		generating := time.Now()
		response, err := complete(ctx, model, system, prompt, options.OnChunk)
		took := time.Since(generating)
		timing.Generate += took
		if err != nil {
			return result, err
		}
		result.Source = Program(response)
		options.emit(Event{Kind: EventProgramWritten, Attempt: attempt, Duration: took})

		*stage = StageRun
		answer, recorder, err := run(ctx, mode, result.Source, options, timing)
		result.Calls = append(result.Calls, recorder.Calls()...)
		if err == nil {
			result.Answer = answer
			result.Planned = planned(recorder)
			return result, pending(ctx, recorder, result.Planned, request, options)
		}
		if stop := unretryable(recorder, err); stop != nil {
			return result, stop
		}
		options.emit(Event{Kind: EventProgramFailed, Attempt: attempt, Err: err.Error()})
		if used := llm.UsageFrom(ctx).Total(); options.MaxTokens > 0 && used >= options.MaxTokens {
			return result, fmt.Errorf("%w: %d of %d tokens used: %w", ErrTokenBudget, used, options.MaxTokens, err)
		}
		lastErr = err
		prompt = repairPrompt(request, result.Source, err)
	}
	return result, fmt.Errorf("gave up after %d attempts: %w", options.Attempts, lastErr)
}

func cacheKey(mode codemode.CodeMode, runner sandbox.Runner, request string, options Options) (program.Key, bool) {
	if options.Cache == nil {
		return program.Key{}, false
	}
	fingerprinter, ok := mode.(codemode.Fingerprinter)
	if !ok {
		return program.Key{}, false
	}
	// An empty fingerprint would scope every capability set together.
	fingerprint := fingerprinter.Fingerprint()
	if fingerprint == "" {
		return program.Key{}, false
	}
	return program.Key{
		Request:      request,
		Language:     runner.Language(),
		Capabilities: fingerprint,
		Caller:       options.CacheCaller,
		Instructions: strings.TrimSpace(options.Instructions),
	}, true
}

// A cache hit may be a merely similar request, so programs naming a write are never reused.
// ponytail: a capability name built at runtime escapes the source scan; the recorded calls still catch it when it runs.
func reusable(result Result) bool {
	for _, call := range result.Calls {
		if !call.ReadOnly {
			return false
		}
	}
	for _, match := range result.Matches {
		if !match.Capability.ReadOnly && strings.Contains(result.Source, match.Capability.Name) {
			return false
		}
	}
	return true
}

// Any cache failure is a miss; a cached program that fails after writing is not regenerated.
func fromCache(ctx context.Context, mode codemode.CodeMode, key program.Key, options Options, timing *Timing) (Result, bool, error) {
	lookup := time.Now()
	entry, found, err := options.Cache.Lookup(ctx, key)
	timing.Cache = time.Since(lookup)
	if err != nil || !found {
		return Result{}, false, nil
	}
	answer, recorder, err := run(ctx, mode, entry.Source, options, timing)
	result := Result{Answer: answer, Source: entry.Source, Cached: true, Calls: recorder.Calls()}
	if err != nil {
		_ = options.Cache.Forget(ctx, entry.ID)
		return result, false, unretryable(recorder, err)
	}
	result.Planned = planned(recorder)
	return result, true, nil
}

func run(ctx context.Context, mode codemode.CodeMode, source string, options Options, timing *Timing) (any, *codemode.Recorder, error) {
	recorder := &codemode.Recorder{Plan: options.Plan, OnCall: func(call codemode.Call) {
		kind := EventCallDone
		if call.Planned {
			kind = EventCallHeld
		}
		options.emit(Event{Kind: kind, Name: call.Name, Duration: call.Duration, Err: call.Error})
		if options.OnCall != nil {
			options.OnCall(call)
		}
	}}
	running := time.Now()
	answer, err := options.Runner.Run(codemode.WithRecorder(ctx, recorder), mode, source)
	timing.Run += time.Since(running)
	if err == nil && options.check != nil {
		err = options.check(answer)
	}
	return answer, recorder, err
}

func pending(ctx context.Context, recorder *codemode.Recorder, held []codemode.Call, request string, options Options) error {
	if !recorder.Pending() {
		return nil
	}
	if options.Plans == nil {
		return &Pending{Plan: Plan{RunID: options.runID, Request: request, Calls: held, Status: PlanPending}}
	}
	plan, err := options.Plans.hold(ctx, options.PlanOwner, options.runID, request, held)
	if err != nil {
		return err
	}
	return &Pending{Plan: plan}
}

func unretryable(recorder *codemode.Recorder, err error) error {
	if permanent := recorder.Permanent(); permanent != nil {
		return fmt.Errorf("%w: %w", ErrNotRetried, permanent)
	}
	if recorder.Wrote() {
		return fmt.Errorf("%w: program failed after a write: %w", ErrNotRetried, err)
	}
	return nil
}

func planned(recorder *codemode.Recorder) []codemode.Call {
	var writes []codemode.Call
	for _, call := range recorder.Calls() {
		if call.Planned {
			writes = append(writes, call)
		}
	}
	return writes
}

func complete(ctx context.Context, model llm.Client, system, prompt string, chunk func(string)) (string, error) {
	if chunk != nil {
		if streamer, ok := model.(llm.Streamer); ok {
			return streamer.CompleteStream(ctx, system, prompt, chunk)
		}
	}
	return model.Complete(ctx, system, prompt)
}

// Tolerates the markdown fences models add even when told not to.
func Program(response string) string {
	source := strings.TrimSpace(response)
	if !strings.HasPrefix(source, "```") {
		return source
	}
	if _, rest, found := strings.Cut(source, "\n"); found {
		source = rest
	}
	if fenced, _, found := strings.Cut(source, "```"); found {
		source = fenced
	}
	return strings.TrimSpace(source)
}

func repairPrompt(request, source string, failure error) string {
	return fmt.Sprintf(`%s

Your previous program failed.

Program:
%s

Error:
%s

Reply with a corrected program.`, request, source, failure)
}

func Answer(value any) string {
	if value == nil {
		return "(no value returned)"
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}
