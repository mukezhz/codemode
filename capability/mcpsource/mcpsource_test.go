package mcpsource

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
	_ "github.com/mukezhz/codemode/sandbox/goja"
)

type listInput struct {
	State string `json:"state"`
	Limit int    `json:"limit,omitempty"`
}

type issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
}

type listOutput struct {
	Issues []issue `json:"issues"`
}

func session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "github"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "listIssues", Description: "Lists issues",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, input listInput) (*mcp.CallToolResult, listOutput, error) {
			return nil, listOutput{Issues: []issue{{Number: 7, Title: "state " + input.State, Labels: []string{"bug"}}}}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "greet"},
		func(_ context.Context, _ *mcp.CallToolRequest, input struct{ Name string }) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hi " + input.Name}}}, nil, nil
		})
	server.AddTool(&mcp.Tool{Name: "vague", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"x": map[string]any{"oneOf": []any{}}},
	}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return &mcp.CallToolResult{}, nil })

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestImportConvertsToolsAndRestrictsThem(t *testing.T) {
	source, err := Import(context.Background(), session(t), "github", "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if _, skipped := source.Skipped["vague"]; !skipped || len(source.Skipped) != 1 {
		t.Fatalf("skipped = %v, want only vague", source.Skipped)
	}

	byName := map[string]capability.Capability{}
	for _, item := range source.Registry.Capabilities {
		byName[item.Name] = item
		if len(item.Roles) != 1 || item.Roles[0] != "engineer" {
			t.Errorf("%s roles = %v, want [engineer]", item.Name, item.Roles)
		}
		if item.ReadOnly {
			t.Errorf("%s trusted the server's read-only hint", item.Name)
		}
	}
	list, ok := byName["github.listIssues"]
	if !ok {
		t.Fatalf("capabilities = %v", byName)
	}
	if list.Input.Fields["state"] != "string" || list.Input.Fields["limit"] != "number" {
		t.Errorf("input = %+v", list.Input)
	}
	item := list.Output.Fields["issues"]
	if !strings.HasPrefix(item, "repeated ") || list.Types[strings.TrimPrefix(item, "repeated ")].Fields["labels"] != "repeated string" {
		t.Errorf("output = %+v, types = %+v", list.Output, list.Types)
	}
	if byName["github.greet"].Output.Fields["text"] != "string" {
		t.Errorf("greet output = %+v, want text", byName["github.greet"].Output)
	}
	if len(source.Registry.ForRole("member").Capabilities) != 0 {
		t.Error("a role that was not granted can see imported tools")
	}
}

func TestExecuteCallsTheTool(t *testing.T) {
	source, err := Import(context.Background(), session(t), "github", "engineer")
	if err != nil {
		t.Fatal(err)
	}
	for rpc, call := range map[string]struct{ input, want string }{
		RPCPrefix("github") + "listIssues": {`{"state":"open"}`, `"title":"state open"`},
		RPCPrefix("github") + "greet":      {`{"Name":"ada"}`, `{"text":"hi ada"}`},
	} {
		want := call.want
		output, err := source.Execute(context.Background(), rpc, json.RawMessage(call.input))
		if err != nil {
			t.Fatalf("%s: %v", rpc, err)
		}
		if !strings.Contains(string(output), want) {
			t.Errorf("%s = %s, want %s", rpc, output, want)
		}
	}
	if _, err := source.Execute(context.Background(), "tasks.v1.TaskService/List", nil); err == nil {
		t.Error("executed an RPC from another source")
	}
}

func TestImportRefusesUnrestrictedTools(t *testing.T) {
	if _, err := Import(context.Background(), session(t), "github", " "); err == nil {
		t.Fatal("imported tools with no role")
	}
}

func TestImportedNamesCollideLoudly(t *testing.T) {
	source, err := Import(context.Background(), session(t), "github", "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capability.Merge(source.Registry, source.Registry); err == nil {
		t.Fatal("merging duplicate capability names succeeded")
	}
}

func TestOneProgramCallsHostAndImportedCapabilities(t *testing.T) {
	source, err := Import(context.Background(), session(t), "github", "engineer")
	if err != nil {
		t.Fatal(err)
	}
	own := capability.Registry{Capabilities: []capability.Capability{{
		Name: "tasks.count", RPC: "tasks.v1.TaskService/Count", Description: "Counts tasks", ReadOnly: true,
		Output: capability.TypeSchema{Fields: map[string]string{"count": "number"}},
	}}}
	merged, err := capability.Merge(own, source.Registry)
	if err != nil {
		t.Fatal(err)
	}
	route := func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
		if strings.HasPrefix(rpc, RPCPrefix("github")) {
			return source.Execute(ctx, rpc, input)
		}
		return json.RawMessage(`{"count":3}`), nil
	}
	mode, err := codemode.New(merged.ForRole("engineer"),
		func(context.Context, string, int) ([]capability.Match, error) { return nil, nil }, route)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	value, err := runner.Run(context.Background(), mode, `
		const { count } = codemode.execute("tasks.count", {});
		const { issues } = codemode.execute("github.listIssues", { state: "open" });
		return count + " tasks, " + issues.length + " issue";`)
	if err != nil {
		t.Fatal(err)
	}
	if value != "3 tasks, 1 issue" {
		t.Fatalf("value = %v", value)
	}
}
