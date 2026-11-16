// todo-mcp serves the todo example to MCP clients over stdio. Stdout belongs to
// the protocol, so diagnostics go to stderr.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/examples/todo/app"
	"github.com/mukezhz/codemode/mcpserver"
	"github.com/mukezhz/codemode/sandbox"

	_ "github.com/mukezhz/codemode/sandbox/goja"
	_ "github.com/mukezhz/codemode/sandbox/lua"
	_ "github.com/mukezhz/codemode/sandbox/starlark"
	_ "github.com/mukezhz/codemode/sandbox/subprocess"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// An MCP client is a program author you do not control: prefer SANDBOX_ISOLATION=process.
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		return err
	}
	defer runner.Close()
	fmt.Fprintf(os.Stderr, "todo-mcp: %s\n", runner.Name())

	server, err := newServer(runner)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

// todos.delete needs approval, which an MCP client cannot give: run_program
// refuses a program that reaches it and names the writes that did not run.
func newServer(runner sandbox.Runner) (*mcp.Server, error) {
	source, err := app.New().Source()
	if err != nil {
		return nil, err
	}
	mode, err := codemode.FromSource(source, capability.Caller{})
	if err != nil {
		return nil, err
	}
	return mcpserver.New(mcpserver.Static(mode), runner, mcpserver.Options{Name: "todo"})
}
