// Package agent runs one code mode request end to end: search for relevant
// capabilities, ask a model for a program that composes them, and run that
// program in the sandbox.
//
// The model only ever learns the capabilities that search selected. The
// executable allowlist stays with the code mode runtime, so a program that asks
// for a capability outside the registry is rejected regardless of the prompt.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/program"
	"github.com/mukezhz/codemode/sandbox"
)

// Options tunes one request.
type Options struct {
	// Limit is the maximum number of capabilities described to the model.
	// Zero applies DefaultLimit.
	Limit int
	// Attempts is how many programs may be generated before giving up. Each
	// attempt after the first is told why the previous one failed. Zero
	// applies DefaultAttempts.
	Attempts int
	// Runner isolates and evaluates each generated program. Required: this
	// package builds nothing from the environment on your behalf, so the
	// engine your programs run in is always a decision you made.
	//
	// Build one with an engine package directly — goja.New(sandbox.Options{}) —
	// or with sandbox.NewFromEnv if you want SANDBOX_ENGINE consulted. The
	// caller closes it.
	Runner sandbox.Runner

	// Instructions are appended to the system prompt, after the engine's own
	// rules and the capability declarations. This is where domain guidance
	// goes: house style for the answer, a policy the program must respect, a
	// hint about which capability to prefer.
	//
	// The engine's rules are not replaceable, because they describe the
	// language and the single binding the program is evaluated against; a
	// prompt without them produces code the sandbox cannot run. To take over
	// the prompt entirely, drive the loop yourself — Runner.Prompt gives you
	// the same base to build on.
	Instructions string

	// Cache serves the program for a request that has been answered before,
	// skipping search and generation entirely. Nil disables caching.
	//
	// This is where nearly all the latency of a repeated request is: writing a
	// program costs a model call, running one costs microseconds. A hit is
	// scoped to the engine's dialect, the runtime's capability fingerprint,
	// and CacheCaller, so a cached program is never served into a world it was
	// not written for.
	//
	// The caller closes it. See package program.
	Cache program.Cache

	// CacheCaller separates cache entries that must not be shared, such as a
	// role or a tenant. Leave it empty only when every caller of this agent
	// sees the same capabilities and the same data.
	CacheCaller string
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
	// Answer is the value the program returned, decoded into Go types.
	Answer any
	// Source is the program that produced Answer.
	Source string
	// Matches are the capabilities that were described to the model.
	Matches []capability.Match
	// Attempts is how many programs were generated, including the one that
	// succeeded. A cached answer generated nothing, so it reports zero.
	Attempts int
	// Cached reports that Source came from the cache. Matches is empty in that
	// case: a cache hit skips capability search along with generation.
	Cached bool
}

// Run answers request by searching mode for capabilities, asking model for a
// program, and running it against mode. Ctx carries cancellation.
func Run(ctx context.Context, mode codemode.CodeMode, model llm.Client, request string, options Options) (Result, error) {
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

	key, caching := cacheKey(mode, runner, request, options)
	if caching {
		if result, hit := fromCache(ctx, mode, options.Cache, key, runner); hit {
			return result, nil
		}
	}

	matches, err := mode.Search(ctx, request, options.Limit)
	if err != nil {
		return Result{}, fmt.Errorf("search: %w", err)
	}
	if len(matches) == 0 {
		return Result{}, errors.New("search matched no capabilities")
	}

	// The engine that will evaluate the program writes the instructions for
	// producing it, so switching engines switches dialects with no other change.
	system := runner.Prompt(matches)
	if instructions := strings.TrimSpace(options.Instructions); instructions != "" {
		system += "\nAdditional instructions:\n" + instructions + "\n"
	}
	prompt := request
	result := Result{Matches: matches}

	// Each failure is fed back so the next program can correct it. Sandbox
	// errors are the model's to fix; transport errors are not.
	var lastErr error
	for attempt := 1; attempt <= options.Attempts; attempt++ {
		result.Attempts = attempt

		response, err := model.Complete(ctx, system, prompt)
		if err != nil {
			return result, err
		}
		result.Source = Program(response)

		answer, err := runner.Run(ctx, mode, result.Source)
		if err == nil {
			result.Answer = answer
			if caching {
				// A cache write that fails costs the next identical request a
				// model call it would have paid anyway, so it must not fail a
				// request that has already been answered.
				_ = options.Cache.Put(ctx, key, result.Source)
			}
			return result, nil
		}
		lastErr = err
		prompt = repairPrompt(request, result.Source, err)
	}
	return result, fmt.Errorf("gave up after %d attempts: %w", options.Attempts, lastErr)
}

// cacheKey builds the key a cached program is stored under, and reports
// whether caching is possible at all. It is not when no cache is configured,
// and not when the runtime cannot fingerprint its capability set: without a
// fingerprint there is no way to tell that a cached program was written for a
// registry that has since changed, and serving it anyway is worse than not
// caching.
func cacheKey(mode codemode.CodeMode, runner sandbox.Runner, request string, options Options) (program.Key, bool) {
	if options.Cache == nil {
		return program.Key{}, false
	}
	fingerprinter, ok := mode.(codemode.Fingerprinter)
	if !ok {
		return program.Key{}, false
	}
	return program.Key{
		Request:      request,
		Language:     runner.Language(),
		Capabilities: fingerprinter.Fingerprint(),
		Caller:       options.CacheCaller,
	}, true
}

// fromCache answers request with a previously generated program. A miss, a
// cache that errors, and a cached program that no longer runs all report no
// hit, so the caller falls through to generation — the cache is an
// optimization, and nothing about it may fail a request.
func fromCache(ctx context.Context, mode codemode.CodeMode, cache program.Cache, key program.Key, runner sandbox.Runner) (Result, bool) {
	entry, found, err := cache.Lookup(ctx, key)
	if err != nil || !found {
		return Result{}, false
	}
	answer, err := runner.Run(ctx, mode, entry.Source)
	if err != nil {
		// The world moved under this program: a capability it called changed
		// shape, or the data it assumed is gone. Drop it rather than serve it
		// again, and let generation produce one that fits.
		_ = cache.Forget(ctx, entry.ID)
		return Result{}, false
	}
	return Result{Answer: answer, Source: entry.Source, Cached: true}, true
}

// Program extracts a program from a model response, tolerating the markdown
// fences models add even when told not to.
func Program(response string) string {
	source := strings.TrimSpace(response)
	if !strings.HasPrefix(source, "```") {
		return source
	}
	// Drop the opening fence and its language tag, then everything from the
	// closing fence onward.
	if _, rest, found := strings.Cut(source, "\n"); found {
		source = rest
	}
	if fenced, _, found := strings.Cut(source, "```"); found {
		source = fenced
	}
	return strings.TrimSpace(source)
}

func repairPrompt(request, source string, failure error) string {
	// The failed program is included so the model corrects it rather than
	// rewriting from scratch and repeating the mistake.
	return fmt.Sprintf(`%s

Your previous program failed.

Program:
%s

Error:
%s

Reply with a corrected program.`, request, source, failure)
}

// Answer renders a result value for display. Strings pass through; anything
// else is rendered as JSON so objects stay readable.
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
