// Package trace makes one request legible.
//
// Code mode has a lot of moving parts between a sentence and an answer: search
// ranks, a prompt is built, a model writes a program, the program calls back
// through a bridge several times, and something is rendered. When an answer is
// wrong, the useful question is *which of those* went wrong — and without a
// trace the honest reply is a shrug.
//
// So every step logs, in order, with timings:
//
//	step=search      query="how many todos do I have" matched=12 offered=[tasks.list ...]
//	step=model       provider=openai/deepseek-chat system_bytes=3401
//	step=model.done  took=4.1s bytes=612
//	step=sandbox     engine=javascript lines=9
//
//	  ── program (javascript) ──────────────────────────────────
//	   1  const counts = codemode.execute("reports.statusCounts", {});
//	   2  const tasks  = codemode.execute("tasks.list", {}).tasks;
//	   …
//	  ──────────────────────────────────────────────────────────
//
//	step=execute     name=tasks.list input={} took=311µs output=248
//	step=sandbox.done took=6ms calls=2
//	step=answer      blocks=3 attempts=1 took=4.3s
//
// Every one of these comes from a decorator around an interface rather than
// from inside the library, which is the point: the seams are interfaces, so
// observing them needs no change to the thing being observed.
//
// The program prints as a block rather than a log field on purpose. It is the
// single most useful artifact when an answer looks wrong, and slog would render
// it as one line with the newlines escaped — technically the same bytes, and
// unreadable.
package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/sandbox"
)

// CodeMode wraps a runtime so every capability call is logged.
//
// It logs failures at Error and successes at Debug, because a working request
// making forty calls is noise until something breaks — and then it is exactly
// what you want.
func CodeMode(inner codemode.CodeMode, log *slog.Logger) codemode.CodeMode {
	return &tracedMode{inner: inner, log: log}
}

type tracedMode struct {
	inner codemode.CodeMode
	log   *slog.Logger
}

// Fingerprint forwards the wrapped runtime's capability fingerprint, when it
// has one.
//
// A wrapper has to forward this explicitly. A runtime stored in an interface
// loses the methods that interface does not name, so a traced runtime that did
// not implement codemode.Fingerprinter would turn program caching off for
// everything behind the trace — silently, because the agent treats a missing
// fingerprint as "caching is not safe here" rather than as an error.
func (mode *tracedMode) Fingerprint() string {
	fingerprinter, ok := mode.inner.(codemode.Fingerprinter)
	if !ok {
		return ""
	}
	return fingerprinter.Fingerprint()
}

func (mode *tracedMode) Search(ctx context.Context, query string, limit int) ([]capability.Match, error) {
	started := time.Now()
	matches, err := mode.inner.Search(ctx, query, limit)
	if err != nil {
		mode.log.Error("step=search", "query", query, "error", err)
		return nil, err
	}

	offered := make([]string, len(matches))
	for index, match := range matches {
		offered[index] = match.Capability.Name
	}
	mode.log.Info("step=search",
		"query", query,
		"limit", limit,
		"offered", offered,
		"closest", closest(matches),
		"took", since(started))
	return matches, nil
}

func (mode *tracedMode) Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	started := time.Now()
	output, err := mode.inner.Execute(ctx, name, input)
	if err != nil {
		// A rejected capability is the security boundary doing its job, and a
		// failing one is usually why an answer looks wrong. Both are worth
		// seeing without turning the level up.
		mode.log.Error("step=execute",
			"name", name,
			"input", truncate(string(input), 200),
			"took", since(started),
			"error", err)
		return nil, err
	}
	mode.log.Debug("step=execute",
		"name", name,
		"input", truncate(string(input), 200),
		"took", since(started),
		"output", len(output))
	return output, nil
}

// Model wraps a client so the prompt and the response are visible.
func Model(inner llm.Client, log *slog.Logger) llm.Client {
	return &tracedModel{inner: inner, log: log}
}

type tracedModel struct {
	inner llm.Client
	log   *slog.Logger
}

func (model *tracedModel) Name() string { return model.inner.Name() }

func (model *tracedModel) Complete(ctx context.Context, system, user string) (string, error) {
	model.log.Info("step=model",
		"provider", model.inner.Name(),
		"system_bytes", len(system),
		"user", truncate(user, 300))
	// The whole prompt is one Debug line rather than several, so copying it out
	// of a terminal gives you something you can replay.
	model.log.Debug("step=prompt", "system", system)

	started := time.Now()
	response, err := model.inner.Complete(ctx, system, user)
	if err != nil {
		model.log.Error("step=model.done", "took", since(started), "error", err)
		return "", err
	}

	model.log.Info("step=model.done", "took", since(started), "bytes", len(response))
	return response, nil
}

// CompleteStream implements llm.Streamer when the wrapped model does.
//
// The wrapper has to forward it explicitly: a client stored in an interface
// loses the methods the interface does not name, so a traced model that did not
// implement Streamer would silently turn streaming off for everything behind
// the trace.
func (model *tracedModel) CompleteStream(ctx context.Context, system, user string, chunk func(string)) (string, error) {
	streamer, ok := model.inner.(llm.Streamer)
	if !ok {
		return model.Complete(ctx, system, user)
	}

	model.log.Info("step=model",
		"provider", model.inner.Name(),
		"system_bytes", len(system),
		"user", truncate(user, 300),
		"streaming", true)
	model.log.Debug("step=prompt", "system", system)

	started := time.Now()
	first := time.Duration(0)
	response, err := streamer.CompleteStream(ctx, system, user, func(piece string) {
		if first == 0 {
			// Time to first chunk is the number streaming actually improves,
			// so it is the one worth logging next to the total.
			first = time.Since(started)
		}
		chunk(piece)
	})
	if err != nil {
		model.log.Error("step=model.done", "took", since(started), "error", err)
		return "", err
	}

	model.log.Info("step=model.done",
		"took", since(started),
		"first_chunk", first.Round(time.Millisecond),
		"bytes", len(response))
	return response, nil
}

// Runner wraps a sandbox engine so the program it is asked to evaluate is
// printed before it runs.
//
// This is the seam worth watching. trace.Model sees what the model *said*,
// which still has its markdown fences; Runner sees what actually reaches the
// interpreter after the agent has stripped them. When those differ, the second
// is the one that explains the answer.
//
// Out receives the formatted program. Nil prints nothing, which is what a test
// wants.
func Runner(inner sandbox.Runner, log *slog.Logger, out io.Writer) sandbox.Runner {
	return &tracedRunner{inner: inner, log: log, out: out}
}

type tracedRunner struct {
	inner sandbox.Runner
	log   *slog.Logger
	out   io.Writer
}

func (runner *tracedRunner) Language() string                   { return runner.inner.Language() }
func (runner *tracedRunner) Name() string                       { return runner.inner.Name() }
func (runner *tracedRunner) Close() error                       { return runner.inner.Close() }
func (runner *tracedRunner) Prompt(m []capability.Match) string { return runner.inner.Prompt(m) }

func (runner *tracedRunner) Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error) {
	source = strings.TrimSpace(source)
	lines := strings.Count(source, "\n") + 1

	runner.log.Info("step=sandbox", "engine", runner.inner.Language(), "lines", lines)
	runner.print(source)

	// Counting through a wrapper rather than reading a field keeps this honest:
	// it is the calls the program actually made, not what it intended.
	counter := &callCounter{CodeMode: mode}
	started := time.Now()
	value, err := runner.inner.Run(ctx, counter, source)
	if err != nil {
		runner.log.Error("step=sandbox.done",
			"took", since(started), "calls", counter.calls.Load(), "error", err)
		return nil, err
	}
	runner.log.Info("step=sandbox.done", "took", since(started), "calls", counter.calls.Load())
	return value, nil
}

// print renders the program with line numbers, so an error naming line 7 can be
// read against line 7.
func (runner *tracedRunner) print(source string) {
	if runner.out == nil {
		return
	}
	const width = 68
	// Repeat by rune count, never slice: the box-drawing character is three
	// bytes, so a byte slice cuts one in half and the terminal shows mojibake.
	header := fmt.Sprintf("┌─ program (%s) ", runner.inner.Language())
	var block strings.Builder
	fmt.Fprintf(&block, "\n  %s%s\n", header,
		strings.Repeat("─", max(0, width-len([]rune(header))+1)))
	for number, line := range strings.Split(source, "\n") {
		fmt.Fprintf(&block, "  │ %3d  %s\n", number+1, line)
	}
	fmt.Fprintf(&block, "  └%s\n\n", strings.Repeat("─", width))

	io.WriteString(runner.out, block.String())
}

// callCounter counts capability calls without disturbing them.
type callCounter struct {
	codemode.CodeMode
	calls atomic.Int64
}

func (counter *callCounter) Execute(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	counter.calls.Add(1)
	return counter.CodeMode.Execute(ctx, name, input)
}

func closest(matches []capability.Match) string {
	if len(matches) == 0 {
		return ""
	}
	return matches[0].Capability.Name
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func since(started time.Time) string {
	return time.Since(started).Round(time.Microsecond).String()
}
