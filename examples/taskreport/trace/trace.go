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

func CodeMode(inner codemode.CodeMode, log *slog.Logger) codemode.CodeMode {
	return &tracedMode{inner: inner, log: log}
}

type tracedMode struct {
	inner codemode.CodeMode
	log   *slog.Logger
}

// Fingerprint forwards the wrapped runtime's fingerprint; without it, program caching silently turns off.
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
		// Rejected or failing calls log without raising the level; they usually explain a wrong answer.
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

// CompleteStream implements llm.Streamer when the wrapped model does; forwarding keeps streaming enabled.
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

func (runner *tracedRunner) print(source string) {
	if runner.out == nil {
		return
	}
	const width = 68
	// Repeat by rune count: box-drawing chars are multibyte.
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
