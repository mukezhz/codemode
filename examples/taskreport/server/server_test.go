package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/codemode/capability"
	taskv1 "github.com/mukezhz/codemode/examples/taskreport/gen/task/v1"
	userv1 "github.com/mukezhz/codemode/examples/taskreport/gen/user/v1"
	coderegistry "github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/goja"

	"github.com/mukezhz/codemode/examples/taskreport/server"
	"github.com/mukezhz/codemode/examples/taskreport/store"
	"github.com/mukezhz/codemode/examples/taskreport/view"
)

// model returns a fixed program and records the system prompt it was given, so
// the chat path is testable without a provider.
type model struct {
	program string
	system  string
}

func (model *model) Name() string { return "test/model" }

func (model *model) Complete(_ context.Context, system, _ string) (string, error) {
	model.system = system
	return model.program, nil
}

func newServer(t *testing.T, author *model) http.Handler {
	t.Helper()

	tasks, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	tasks.SetClock(func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) })
	t.Cleanup(func() { tasks.Close() })

	tasks.Create("ada", "Ship the endpoint", "platform", "high", "2026-08-09", nil)
	tasks.Create("ada", "Write the guide", "docs", "low", "2026-08-30", nil)
	done, _ := tasks.Create("ada", "Migrate the build", "platform", "medium", "", nil)
	tasks.Complete(store.Only("ada"), done.ID)
	tasks.Create("linus", "Record a demo", "docs", "medium", "", nil)

	taskFiles, err := coderegistry.FromFiles(taskv1.File_task_proto)
	if err != nil {
		t.Fatal(err)
	}
	userFiles, err := coderegistry.FromFiles(userv1.File_user_proto)
	if err != nil {
		t.Fatal(err)
	}
	full, err := capability.Merge(taskFiles, userFiles)
	if err != nil {
		t.Fatal(err)
	}

	runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second, MaxCalls: 60})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })

	options := server.Options{
		Store:  tasks,
		Full:   full,
		Admins: []string{"grace"},
		Runner: runner,
	}
	if author != nil {
		options.Model = author
	}

	built, err := server.New(options)
	if err != nil {
		t.Fatal(err)
	}
	return built.Handler()
}

func do(t *testing.T, handler http.Handler, method, path, body, user, role string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		request.Header.Set("X-Demo-User", user)
		request.Header.Set("X-Demo-Role", role)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return value
}

// The session tells the browser what it may show, and it must agree with what
// the server will actually run — otherwise the UI offers buttons that fail.
func TestSessionReportsTheScopedSurface(t *testing.T) {
	handler := newServer(t, nil)

	member := decode[struct {
		Role         string   `json:"role"`
		Capabilities []string `json:"capabilities"`
		Withheld     int      `json:"withheld"`
	}](t, do(t, handler, "GET", "/api/session", "", "ada", "member"))

	if member.Role != "member" || member.Withheld != 4 {
		t.Fatalf("session = %+v, want a member with 4 capabilities withheld", member)
	}
	for _, name := range member.Capabilities {
		if name == "tasks.assign" || name == "users.list" {
			t.Fatalf("a member's session offered %q", name)
		}
	}

	admin := decode[struct {
		Withheld int `json:"withheld"`
	}](t, do(t, handler, "GET", "/api/session", "", "grace", "admin"))
	if admin.Withheld != 0 {
		t.Fatalf("an admin had %d capabilities withheld", admin.Withheld)
	}
}

// The board is scoped the same way the programs are, because both go through
// the same runtime rather than one bypassing it.
func TestBoardIsScopedToTheCaller(t *testing.T) {
	handler := newServer(t, nil)

	mine := decode[struct {
		Tasks []store.Task `json:"tasks"`
	}](t, do(t, handler, "GET", "/api/tasks", "", "ada", "member"))
	if len(mine.Tasks) != 3 {
		t.Fatalf("ada sees %d tasks, want 3", len(mine.Tasks))
	}
	for _, task := range mine.Tasks {
		if task.Owner != "ada" {
			t.Fatalf("ada's board showed a task owned by %q", task.Owner)
		}
	}

	everyone := decode[struct {
		Tasks []store.Task `json:"tasks"`
	}](t, do(t, handler, "GET", "/api/tasks", "", "grace", "admin"))
	if len(everyone.Tasks) != 4 {
		t.Fatalf("an admin sees %d tasks, want 4", len(everyone.Tasks))
	}
}

// Knowing another user's task ID is not access, over HTTP either.
func TestBoardRejectsAnotherUsersTask(t *testing.T) {
	handler := newServer(t, nil)

	everyone := decode[struct {
		Tasks []store.Task `json:"tasks"`
	}](t, do(t, handler, "GET", "/api/tasks", "", "grace", "admin"))

	var theirs int64
	for _, task := range everyone.Tasks {
		if task.Owner == "linus" {
			theirs = task.ID
		}
	}
	if theirs == 0 {
		t.Fatal("no task owned by linus in the fixture")
	}

	response := do(t, handler, "POST", "/api/tasks/"+strconv.FormatInt(theirs, 10)+"/complete", "", "ada", "member")
	if response.Code == http.StatusOK {
		t.Fatal("ada completed a task belonging to linus")
	}
}

// The whole point of the chat: one turn, one program, a document to render.
func TestChatRendersAViewDocument(t *testing.T) {
	author := &model{program: `
		const counts = codemode.execute("reports.statusCounts", {});
		const projects = codemode.execute("reports.byProject", {}).groups;
		const overdue = codemode.execute("reports.overdue", {}).tasks;

		return {
			title: "Where things stand",
			blocks: [
				{ type: "stats", items: [
					{ label: "Open", value: counts.open },
					{ label: "Overdue", value: counts.overdue, tone: "bad" }
				]},
				{ type: "bar", heading: "Open by project",
				  series: projects.map(p => ({ label: p.name, value: p.open })) },
				{ type: "table", heading: "Overdue", columns: ["Task", "Due"],
				  rows: overdue.map(t => [t.title, t.due_date]) },
				{ type: "text", text: "One task is past its due date." }
			]
		};
	`}

	handler := newServer(t, author)
	response := do(t, handler, "POST", "/api/chat", `{"request":"how are things?"}`, "ada", "member")
	if response.Code != http.StatusOK {
		t.Fatalf("chat returned %d: %s", response.Code, response.Body)
	}

	result := decode[struct {
		Document view.Document `json:"document"`
		Program  string        `json:"program"`
		Attempts int           `json:"attempts"`
	}](t, response)

	if result.Document.Title != "Where things stand" || len(result.Document.Blocks) != 4 {
		t.Fatalf("document = %+v", result.Document)
	}
	// Values came from the caller's own rows, not the workspace.
	stats := result.Document.Blocks[0]
	if stats.Items[0].Value != 2 || stats.Items[1].Value != 1 {
		t.Fatalf("stats = %+v, want ada's two open and one overdue", stats.Items)
	}
	// The program is returned so a wrong answer is debuggable.
	if !strings.Contains(result.Program, "reports.statusCounts") {
		t.Fatalf("program = %q", result.Program)
	}

	// The view instructions have to reach the model, or it answers in prose.
	if !strings.Contains(author.system, `{"type":"bar"`) {
		t.Fatalf("system prompt is missing the view schema:\n%s", author.system)
	}
	// And so do the caller's, or a withheld capability produces a confident
	// wrong answer instead of an honest refusal.
	if !strings.Contains(author.system, "You are answering for ada, a member") {
		t.Fatalf("system prompt does not tell the model whose data this is:\n%s", author.system)
	}
	// And the engine's own rules must survive alongside them.
	if !strings.Contains(author.system, "codemode.execute") {
		t.Fatalf("the view instructions displaced the engine's rules:\n%s", author.system)
	}
}

// A program that returns something unrenderable must fail loudly with the
// source attached, not paint a blank panel.
func TestChatRejectsAnUnrenderableResult(t *testing.T) {
	handler := newServer(t, &model{program: `return { blocks: [{ type: "pie", series: [] }] };`})

	response := do(t, handler, "POST", "/api/chat", `{"request":"chart it"}`, "ada", "member")
	if response.Code == http.StatusOK {
		t.Fatal("chat accepted an unknown block type")
	}
	if !strings.Contains(response.Body.String(), "unknown type") {
		t.Fatalf("body = %s, want the schema error", response.Body)
	}
}

// A member's program cannot reach an admin capability even by naming it: the
// registry the chat runs against was narrowed before the program ran.
func TestChatCannotReachWithheldCapabilities(t *testing.T) {
	handler := newServer(t, &model{program: `
		const groups = codemode.execute("reports.byOwner", {}).groups;
		return { blocks: [{ type: "text", text: String(groups.length) }] };
	`})

	response := do(t, handler, "POST", "/api/chat", `{"request":"team workload"}`, "ada", "member")
	if response.Code == http.StatusOK {
		t.Fatal("a member's program reached reports.byOwner")
	}
	if !strings.Contains(response.Body.String(), "reports.byOwner") {
		t.Fatalf("body = %s, want it to name the rejected capability", response.Body)
	}

	// The same program is fine for an administrator.
	if code := do(t, handler, "POST", "/api/chat", `{"request":"team workload"}`, "grace", "admin").Code; code != http.StatusOK {
		t.Fatalf("an admin got %d for reports.byOwner", code)
	}
}

// Without a provider the board still works; only the chat is unavailable, and
// it says so rather than failing obscurely.
func TestChatIsUnavailableWithoutAModel(t *testing.T) {
	handler := newServer(t, nil)
	response := do(t, handler, "POST", "/api/chat", `{"request":"hi"}`, "ada", "member")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", response.Code)
	}
	if !strings.Contains(response.Body.String(), "no model is configured") {
		t.Fatalf("body = %s", response.Body)
	}
}

// The two instruction sets are per role, so a member and an administrator are
// told different things about their own reach.
func TestSystemPromptDescribesTheCallersReach(t *testing.T) {
	program := `return { blocks: [{ type: "text", text: "ok" }] };`

	for _, test := range []struct{ user, role, want, notWant string }{
		{"ada", "member", "only ada's own tasks", "whole workspace"},
		{"grace", "admin", "whole workspace", "outside what you can see"},
	} {
		author := &model{program: program}
		handler := newServer(t, author)
		if code := do(t, handler, "POST", "/api/chat", `{"request":"anything"}`, test.user, test.role).Code; code != http.StatusOK {
			t.Fatalf("%s got %d", test.user, code)
		}
		if !strings.Contains(author.system, test.want) {
			t.Errorf("%s's prompt is missing %q", test.user, test.want)
		}
		if strings.Contains(author.system, test.notWant) {
			t.Errorf("%s's prompt wrongly contains %q", test.user, test.notWant)
		}
	}
}
