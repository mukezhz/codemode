package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/mcpserver"
	"github.com/mukezhz/codemode/sandbox"
)

func TestAnMCPClientCannotDeleteWithoutApproval(t *testing.T) {
	ctx := context.Background()
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	server, err := newServer(runner)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "run_program", Arguments: mcpserver.RunInput{Source: `
		const { todo } = codemode.execute("todos.create", { title: "temporary" });
		codemode.execute("todos.delete", { id: todo.id });
		return "deleted";`}})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !result.IsError || !strings.Contains(text, "did not run: todos.delete") {
		t.Fatalf("result = %q; a delete needing approval must not report success", text)
	}

	listed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "run_program", Arguments: mcpserver.RunInput{Source: `return codemode.execute("todos.list", {}).todos.length;`}})
	if err != nil || listed.IsError {
		t.Fatalf("list: %v %+v", err, listed)
	}
	if structured, _ := listed.StructuredContent.(map[string]any); structured["value"] != float64(1) {
		t.Fatalf("value = %v; the todo created before the held delete must still exist", listed.StructuredContent)
	}
}
