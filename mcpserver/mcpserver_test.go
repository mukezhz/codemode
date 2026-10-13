package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
	_ "github.com/mukezhz/codemode/sandbox/goja"
)

func connect(t *testing.T, options Options) *mcp.ClientSession {
	t.Helper()
	list := capability.Capability{
		Name: "todos.list", RPC: "todo.v1.TodoService/List", Description: "Lists todos", ReadOnly: true,
		Output: capability.TypeSchema{Fields: map[string]string{"todos": "repeated string"}},
	}
	registry := capability.Registry{Capabilities: []capability.Capability{list}}
	mode, err := codemode.New(registry,
		func(context.Context, string, int) ([]capability.Match, error) {
			return []capability.Match{{Capability: list}}, nil
		},
		func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"todos":["a","b"]}`), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })

	server, err := New(mode, runner, options)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func call(t *testing.T, session *mcp.ClientSession, tool string, arguments any) (*mcp.CallToolResult, string) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, content := range result.Content {
		if content, ok := content.(*mcp.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	return result, text.String()
}

func TestSearchReturnsTheRunnersPrompt(t *testing.T) {
	result, text := call(t, connect(t, Options{}), "search_capabilities", SearchInput{Query: "todos"})
	if result.IsError || !strings.Contains(text, `codemode.execute(name: "todos.list"`) {
		t.Fatalf("result = %s", text)
	}
}

func TestRunReturnsValueAndCalls(t *testing.T) {
	result, text := call(t, connect(t, Options{}), "run_program",
		RunInput{Source: `return codemode.execute("todos.list", {}).todos.length;`})
	if result.IsError {
		t.Fatal(text)
	}
	var output RunOutput
	encoded, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatal(err)
	}
	if output.Value != float64(2) || len(output.Calls) != 1 || output.Calls[0].Name != "todos.list" {
		t.Fatalf("output = %+v", output)
	}
}

func TestRunReportsProgramErrorsToTheModel(t *testing.T) {
	for name, source := range map[string]string{
		"unregistered": `return codemode.execute("todos.purge", {});`,
		"runaway":      `while (true) {}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, text := call(t, connect(t, Options{}), "run_program", RunInput{Source: source})
			if !result.IsError || text == "" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestRunRejectsOversizedSource(t *testing.T) {
	result, text := call(t, connect(t, Options{MaxSourceBytes: 10}), "run_program",
		RunInput{Source: `return "this is too long";`})
	if !result.IsError || !strings.Contains(text, "byte limit") {
		t.Fatalf("result = %s", text)
	}
}
