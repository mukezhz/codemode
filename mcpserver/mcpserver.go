// Package mcpserver exposes a CodeMode to MCP clients as two tools: the client's
// model searches for capabilities, then writes one program that composes them.
package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
)

type Options struct {
	Name    string
	Version string

	DefaultLimit   int
	MaxLimit       int
	MaxSourceBytes int
}

const (
	DefaultSearchLimit    = 8
	DefaultMaxSearchLimit = 25
	DefaultMaxSourceBytes = 64 << 10
)

type SearchInput struct {
	Query string `json:"query" jsonschema:"what the program needs to do, in plain words"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many capabilities to return"`
}

type RunInput struct {
	Source string `json:"source" jsonschema:"the program body, in the language search_capabilities described"`
}

type Call struct {
	Name       string `json:"name"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type RunOutput struct {
	Value any    `json:"value"`
	Calls []Call `json:"calls"`
	// Set when a failed program had already called a write, which may have committed.
	Wrote bool `json:"wrote,omitempty"`
}

// Runs on every call; build the runtime from request.Extra. Over stdio Extra is nil; use Static.
type ModeFunc func(ctx context.Context, request *mcp.CallToolRequest) (codemode.CodeMode, error)

func Static(mode codemode.CodeMode) ModeFunc {
	return func(context.Context, *mcp.CallToolRequest) (codemode.CodeMode, error) { return mode, nil }
}

// No tool argument can widen a runtime's registry.
func New(modeFor ModeFunc, runner sandbox.Runner, options Options) (*mcp.Server, error) {
	if modeFor == nil || runner == nil {
		return nil, errors.New("mcpserver: modeFor and runner are required")
	}
	resolve := func(ctx context.Context, request *mcp.CallToolRequest) (codemode.CodeMode, error) {
		mode, err := modeFor(ctx, request)
		if err == nil && mode == nil {
			err = errors.New("no runtime for this caller")
		}
		return mode, err
	}
	if options.Name == "" {
		options.Name = "codemode"
	}
	if options.DefaultLimit == 0 {
		options.DefaultLimit = DefaultSearchLimit
	}
	if options.MaxLimit == 0 {
		options.MaxLimit = DefaultMaxSearchLimit
	}
	if options.MaxSourceBytes == 0 {
		options.MaxSourceBytes = DefaultMaxSourceBytes
	}

	server := mcp.NewServer(&mcp.Implementation{Name: options.Name, Version: options.Version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_capabilities",
		Description: fmt.Sprintf("Call this first. Returns the %s capabilities relevant to a task, with their "+
			"signatures and the rules for writing a program that calls them with run_program.", runner.Language()),
	}, func(ctx context.Context, request *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
		mode, err := resolve(ctx, request)
		if err != nil {
			return nil, nil, err
		}
		limit := input.Limit
		if limit <= 0 {
			limit = options.DefaultLimit
		}
		matches, err := mode.Search(ctx, input.Query, min(limit, options.MaxLimit))
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: runner.Prompt(matches)}}}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_program",
		Description: fmt.Sprintf("Runs one %s program in a sandbox and returns its value. The program may only call "+
			"capabilities returned by search_capabilities, through codemode.execute. On error, fix the program and retry, "+
			"unless the error says a write already ran: then check its effect before running anything again.",
			runner.Language()),
	}, func(ctx context.Context, request *mcp.CallToolRequest, input RunInput) (*mcp.CallToolResult, RunOutput, error) {
		if len(input.Source) > options.MaxSourceBytes {
			return nil, RunOutput{}, fmt.Errorf("program is %d bytes, over the %d byte limit", len(input.Source), options.MaxSourceBytes)
		}
		mode, err := resolve(ctx, request)
		if err != nil {
			return nil, RunOutput{}, err
		}
		recorder := &codemode.Recorder{}
		value, err := runner.Run(codemode.WithRecorder(ctx, recorder), mode, input.Source)
		output := RunOutput{Value: value, Calls: []Call{}}
		for _, call := range recorder.Calls() {
			output.Calls = append(output.Calls, Call{Name: call.Name, Error: call.Error, DurationMS: call.Duration.Milliseconds()})
		}
		if err == nil {
			return nil, output, nil
		}
		// A client blind to a committed write would repeat it.
		message := err.Error()
		if permanent := recorder.Permanent(); permanent != nil {
			message += "\n\nRewriting the program cannot fix this: " + permanent.Error()
		}
		if output.Wrote = recorder.Wrote(); output.Wrote {
			message += "\n\nA write already ran before the failure and may have committed. Do not rerun this program; check its effect first."
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}, output, nil
	})

	return server, nil
}
