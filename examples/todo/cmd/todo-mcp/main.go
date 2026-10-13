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
	todov1 "github.com/mukezhz/codemode/examples/todo/gen/todo/v1"
	"github.com/mukezhz/codemode/mcpserver"
	coderegistry "github.com/mukezhz/codemode/registry"
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
	registry, err := coderegistry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		return err
	}
	mode, err := codemode.New(registry, capability.Keyword(registry), app.New().Execute)
	if err != nil {
		return err
	}
	// An MCP client is a program author you do not control: prefer SANDBOX_ISOLATION=process.
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		return err
	}
	defer runner.Close()
	fmt.Fprintf(os.Stderr, "todo-mcp: %s\n", runner.Name())

	server, err := mcpserver.New(mcpserver.Static(mode), runner, mcpserver.Options{Name: "todo"})
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}
