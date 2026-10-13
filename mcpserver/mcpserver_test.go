package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

	server, err := New(Static(mode), runner, options)
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

type withHeader struct {
	user string
	next http.RoundTripper
}

func (transport withHeader) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("X-User", transport.user)
	return transport.next.RoundTrip(request)
}

// One HTTP MCP server, several users: each tool call gets the caller's own
// registry, and a role change applies on the next call.
func TestEachCallSeesItsCallersCapabilities(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		{Name: "deals.list", RPC: "deals/List", Description: "Lists deals", ReadOnly: true, Permissions: []string{"deals.read"}},
		{Name: "deals.purge", RPC: "deals/Purge", Description: "Deletes every deal", Permissions: []string{"deals.delete"}},
	}}
	var mutex sync.Mutex
	granted := map[string][]string{"ada": {"deals.read"}, "grace": {"deals.manage"}}
	execute := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"done":true}`), nil
	}
	modeFor := func(_ context.Context, request *mcp.CallToolRequest) (codemode.CodeMode, error) {
		mutex.Lock()
		permissions, ok := granted[request.Extra.Header.Get("X-User")]
		mutex.Unlock()
		if !ok {
			return nil, errors.New("unknown user")
		}
		allowed := full.For(capability.Caller{Permissions: permissions})
		return codemode.New(allowed, capability.Keyword(allowed), execute)
	}
	runner, err := sandbox.New(sandbox.Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })
	server, err := New(modeFor, runner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(endpoint.Close)

	sessionFor := func(user string) *mcp.ClientSession {
		client := &http.Client{Transport: withHeader{user: user, next: http.DefaultTransport}}
		session, err := mcp.NewClient(&mcp.Implementation{Name: user}, nil).Connect(context.Background(),
			&mcp.StreamableClientTransport{Endpoint: endpoint.URL, HTTPClient: client, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	purge := RunInput{Source: `return codemode.execute("deals.purge", {}).done;`}

	ada, grace := sessionFor("ada"), sessionFor("grace")
	if result, _ := call(t, grace, "run_program", purge); result.IsError {
		t.Fatal("a caller with deals.manage could not purge")
	}
	if result, text := call(t, ada, "run_program", purge); !result.IsError || !strings.Contains(text, "not registered") {
		t.Fatalf("a read-only caller purged: %s", text)
	}
	if _, text := call(t, ada, "search_capabilities", SearchInput{Query: "deals"}); strings.Contains(text, "deals.purge") {
		t.Fatalf("a read-only caller was shown deals.purge:\n%s", text)
	}

	mutex.Lock()
	granted["ada"] = []string{"deals.manage"} // an admin edits ada's role mid-session
	mutex.Unlock()
	if result, text := call(t, ada, "run_program", purge); result.IsError {
		t.Fatalf("the role change did not apply on the next call: %s", text)
	}

	if result, text := call(t, sessionFor("mallory"), "run_program", purge); !result.IsError || !strings.Contains(text, "unknown user") {
		t.Fatalf("an unknown caller ran a program: %s", text)
	}
}
