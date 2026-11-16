// combined serves one program from three sources at once: typed Go functions,
// a protobuf service and an MCP server's tools, joined with codemode.Combine.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/capability/gosource"
	"github.com/mukezhz/codemode/capability/mcpsource"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	coderegistry "github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"

	todo "github.com/mukezhz/codemode/examples/todo/app"
	todov1 "github.com/mukezhz/codemode/examples/todo/gen/todo/v1"

	_ "github.com/mukezhz/codemode/llm/claude"
	_ "github.com/mukezhz/codemode/llm/openai"
	_ "github.com/mukezhz/codemode/sandbox/goja"
)

// Notes are this application's own capabilities, from Go.
type Note struct {
	Text string `json:"text"`
}

type Notes struct {
	mutex sync.Mutex
	notes []Note
}

type AddNote struct {
	Text string `json:"text" desc:"the note, one or two sentences"`
}

type NoteList struct {
	Notes []Note `json:"notes"`
}

func (notes *Notes) Add(_ context.Context, request AddNote) (NoteList, error) {
	if strings.TrimSpace(request.Text) == "" {
		return NoteList{}, errors.New("text is required")
	}
	notes.mutex.Lock()
	defer notes.mutex.Unlock()
	notes.notes = append(notes.notes, Note(request))
	return NoteList{Notes: append([]Note{}, notes.notes...)}, nil
}

func (notes *Notes) Source() (codemode.Source, error) {
	return gosource.New(gosource.Func("notes.add", "Saves a note and returns every note so far.", gosource.Public(), notes.Add))
}

// The calendar stands in for a service another team runs, in any language: it
// speaks MCP, and codemode imports its tools.
type EventsInput struct {
	Day string `json:"day" jsonschema:"YYYY-MM-DD"`
}

type Event struct {
	Title string `json:"title"`
	Time  string `json:"time"`
}

type EventsOutput struct {
	Events []Event `json:"events"`
}

func calendar(ctx context.Context) (*mcp.ClientSession, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "calendar"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "events", Description: "Lists the events on one day."},
		func(_ context.Context, _ *mcp.CallToolRequest, input EventsInput) (*mcp.CallToolResult, EventsOutput, error) {
			return nil, EventsOutput{Events: []Event{{Title: "Launch review", Time: "10:00"}, {Title: "Write the docs", Time: "14:00"}}}, nil
		})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		return nil, err
	}
	return mcp.NewClient(&mcp.Implementation{Name: "combined"}, nil).Connect(ctx, clientTransport, nil)
}

func sources(ctx context.Context) (codemode.Source, func(), error) {
	notes, err := (&Notes{}).Source()
	if err != nil {
		return codemode.Source{}, nil, err
	}

	registry, err := coderegistry.FromFiles(todov1.File_todo_proto)
	if err != nil {
		return codemode.Source{}, nil, err
	}
	todos := todo.New()
	todos.Create(ctx, todo.CreateRequest{Title: "Write the docs"})
	todos.Create(ctx, todo.CreateRequest{Title: "Book the venue"})
	fromProto := codemode.Source{Registry: registry, Execute: todos.Execute}

	session, err := calendar(ctx)
	if err != nil {
		return codemode.Source{}, nil, err
	}
	// Imported tools arrive restricted to a role the host names, and count as writes.
	events, err := mcpsource.Import(ctx, session, "calendar", "member")
	if err != nil {
		session.Close()
		return codemode.Source{}, nil, err
	}

	all, err := codemode.Combine(notes, fromProto, events)
	if err != nil {
		session.Close()
		return codemode.Source{}, nil, err
	}
	return all, func() { session.Close() }, nil
}

const program = `
const { todos } = codemode.execute("todos.list", {});
const { events } = codemode.execute("calendar.events", { day: "2026-11-16" });
const open = todos.filter(todo => !todo.completed).map(todo => todo.title);
const busy = events.filter(event => open.includes(event.title)).map(event => event.title + " at " + event.time);
const { notes } = codemode.execute("notes.add", { text: "Scheduled today: " + (busy.join(", ") || "nothing on the list") });
return notes[notes.length - 1].text;`

func main() {
	request := flag.String("request", "which of my open todos are on today's calendar? note it down", "what to ask")
	flag.Parse()
	ctx := context.Background()

	all, closeSources, err := sources(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer closeSources()
	mode, err := codemode.FromSource(all, capability.Caller{Roles: []string{"member"}})
	if err != nil {
		log.Fatal(err)
	}
	runner, err := sandbox.NewFromEnv(sandbox.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer runner.Close()

	fmt.Println("capabilities")
	for _, item := range all.Registry.Capabilities {
		fmt.Printf("  %-16s %s\n", item.Name, item.RPC)
	}

	model, err := llm.NewFromEnv()
	if errors.Is(err, llm.ErrNoProvider) {
		value, err := runner.Run(ctx, mode, program)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\nprogram (fixed; set ANTHROPIC_API_KEY or OPENAI_API_KEY to have one written)\n%s\n\nanswer\n  %v\n", program, value)
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	result, err := agent.Run(ctx, mode, model, *request, agent.Options{Runner: runner})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nprogram\n%s\n\nanswer\n  %s\n", result.Source, agent.Answer(result.Answer))
}
