// Package agent runs one code mode request end to end: search for relevant
// capabilities, ask a model for a program that composes them, and run that
// program in the sandbox.
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

// Options tunes one request.
type Options struct {
	// Limit is the maximum number of capabilities described to the model; zero applies DefaultLimit.
	Limit int
	// Attempts is how many programs may be generated before giving up; zero applies DefaultAttempts.
	Attempts int
	// Runner evaluates each generated program. Required; the caller closes it.
	Runner sandbox.Runner
	// Instructions are appended to the system prompt after the engine's rules.
	Instructions string
	// Cache serves programs for repeated requests; nil disables caching. The caller closes it.
	Cache program.Cache
	// CacheCaller separates cache entries that must not be shared, such as a role or tenant.
	CacheCaller string
	// OnChunk receives the program as the model streams it, including repair
	// attempts; nil disables streaming. It must not block.
	OnChunk func(string)
}

// Defaults applied when the matching Options field is zero.
const (
	DefaultLimit    = 5
	DefaultAttempts = 3
)

// checked validates options and applies defaults.
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

// Result records what one request produced.
type Result struct {
	Answer any
	Source string
	// Matches are the capabilities described to the model; empty on a cache hit.
	Matches []capability.Match
	// Attempts is how many programs were generated; zero on a cache hit.
	Attempts int
	Cached   bool
	Timing   Timing
}

// Timing breaks a request's latency down by stage.
type Timing struct {
	Total    time.Duration
	Cache    time.Duration
	Search   time.Duration
	Generate time.Duration
	// Run sums every sandbox run, including stale cached programs and failed attempts.
	Run time.Duration
}

// String renders a timing breakdown for a log line.
func (timing Timing) String() string {
	return fmt.Sprintf("total=%s cache=%s search=%s generate=%s run=%s",
		timing.Total.Round(time.Microsecond), timing.Cache.Round(time.Microsecond),
		timing.Search.Round(time.Microsecond), timing.Generate.Round(time.Microsecond),
		timing.Run.Round(time.Microsecond))
}

// Run answers request by searching mode for capabilities, asking model for a
// program, and running it against mode. Ctx carries cancellation.
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

	runner := options.Runner
	var timing Timing
	finish := func(result Result, err error) (Result, error) {
		timing.Total = time.Since(started)
		result.Timing = timing
		return result, err
	}

	key, caching := cacheKey(mode, runner, request, options)
	if caching {
		if result, hit := fromCache(ctx, mode, options.Cache, key, runner, &timing); hit {
			return finish(result, nil)
		}
	}

	search := time.Now()
	matches, err := mode.Search(ctx, request, options.Limit)
	timing.Search = time.Since(search)
	if err != nil {
		return finish(Result{}, fmt.Errorf("search: %w", err))
	}
	if len(matches) == 0 {
		return finish(Result{}, errors.New("search matched no capabilities"))
	}

	result, err := generate(ctx, mode, model, request, matches, options, &timing)
	if err != nil {
		return finish(result, err)
	}
	if caching {
		// A failed cache write must not fail an answered request.
		_ = options.Cache.Put(ctx, key, result.Source)
	}
	return finish(result, nil)
}

// generate asks the model for a program and runs it, feeding each failure back
// so the next attempt can correct it. Sandbox errors are the model's to fix;
// transport errors are not.
func generate(ctx context.Context, mode codemode.CodeMode, model llm.Client, request string, matches []capability.Match, options Options, timing *Timing) (Result, error) {
	system := options.Runner.Prompt(matches)
	if instructions := strings.TrimSpace(options.Instructions); instructions != "" {
		system += "\nAdditional instructions:\n" + instructions + "\n"
	}
	prompt := request
	result := Result{Matches: matches}

	var lastErr error
	for attempt := 1; attempt <= options.Attempts; attempt++ {
		result.Attempts = attempt

		generating := time.Now()
		response, err := complete(ctx, model, system, prompt, options.OnChunk)
		timing.Generate += time.Since(generating)
		if err != nil {
			return result, err
		}
		result.Source = Program(response)

		running := time.Now()
		answer, err := options.Runner.Run(ctx, mode, result.Source)
		timing.Run += time.Since(running)
		if err == nil {
			result.Answer = answer
			return result, nil
		}
		lastErr = err
		prompt = repairPrompt(request, result.Source, err)
	}
	return result, fmt.Errorf("gave up after %d attempts: %w", options.Attempts, lastErr)
}

// cacheKey builds the cache key, reporting false when there is no cache or the
// runtime cannot fingerprint its capability set.
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
	}, true
}

// fromCache answers with a cached program; any cache failure reports a miss.
func fromCache(ctx context.Context, mode codemode.CodeMode, cache program.Cache, key program.Key, runner sandbox.Runner, timing *Timing) (Result, bool) {
	lookup := time.Now()
	entry, found, err := cache.Lookup(ctx, key)
	timing.Cache = time.Since(lookup)
	if err != nil || !found {
		return Result{}, false
	}
	running := time.Now()
	answer, err := runner.Run(ctx, mode, entry.Source)
	timing.Run += time.Since(running)
	if err != nil {
		// Stale program: drop it and regenerate.
		_ = cache.Forget(ctx, entry.ID)
		return Result{}, false
	}
	return Result{Answer: answer, Source: entry.Source, Cached: true}, true
}

// complete asks the model for a program, streaming when both sides support it.
func complete(ctx context.Context, model llm.Client, system, prompt string, chunk func(string)) (string, error) {
	if chunk != nil {
		if streamer, ok := model.(llm.Streamer); ok {
			return streamer.CompleteStream(ctx, system, prompt, chunk)
		}
	}
	return model.Complete(ctx, system, prompt)
}

// Program extracts a program from a model response, tolerating the markdown
// fences models add even when told not to.
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

// Answer renders a result value for display, as JSON unless it is a string.
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
