package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/examples/taskreport/app"
	"github.com/mukezhz/codemode/examples/taskreport/server"
	"github.com/mukezhz/codemode/examples/taskreport/store"
)

func fromGo(t *testing.T) func(*server.Options) {
	return func(options *server.Options) {
		source, err := app.New(options.Store, options.Admins...).Source()
		if err != nil {
			t.Fatal(err)
		}
		options.Full, options.Execute = source.Registry, source.Execute
	}
}

func adasTasks(t *testing.T, handler http.Handler) []store.Task {
	t.Helper()
	return decode[struct {
		Tasks []store.Task `json:"tasks"`
	}](t, do(t, handler, "GET", "/api/tasks", "", "ada", "member")).Tasks
}

type chatPending struct {
	Pending *struct {
		ID    string `json:"id"`
		Calls []struct {
			Name string `json:"name"`
		} `json:"calls"`
	} `json:"pending"`
}

func TestChatHoldsADeleteUntilTheUserApprovesIt(t *testing.T) {
	for name, configure := range map[string][]func(*server.Options){"proto": nil, "go": {fromGo(t)}} {
		t.Run(name, func(t *testing.T) {
			author := &model{program: `
				const { tasks } = codemode.execute("tasks.list", { status: "open" });
				for (const task of tasks) codemode.execute("tasks.delete", { id: task.id });
				return "deleted " + tasks.length;`}
			handler := newServer(t, author, configure...)
			before := len(adasTasks(t, handler))

			response := do(t, handler, "POST", "/api/chat", `{"request":"delete my open tasks"}`, "ada", "member")
			if response.Code != http.StatusOK {
				t.Fatalf("chat returned %d: %s", response.Code, response.Body)
			}
			held := decode[chatPending](t, response).Pending
			if held == nil || held.ID == "" || len(held.Calls) != 2 || held.Calls[0].Name != "tasks.delete" {
				t.Fatalf("pending = %+v; want both deletes held", held)
			}
			if len(adasTasks(t, handler)) != before {
				t.Fatal("a delete ran before it was approved")
			}

			if response := do(t, handler, "POST", "/api/plans/"+held.ID+"/approve", "", "linus", "member"); response.Code != http.StatusNotFound {
				t.Fatalf("another user approved ada's plan: %d", response.Code)
			}
			if response := do(t, handler, "POST", "/api/plans/"+held.ID+"/approve", "", "ada", "member"); response.Code != http.StatusOK {
				t.Fatalf("approve returned %d: %s", response.Code, response.Body)
			}
			if left := len(adasTasks(t, handler)); left != before-2 {
				t.Fatalf("%d tasks left, want %d", left, before-2)
			}
			if response := do(t, handler, "POST", "/api/plans/"+held.ID+"/approve", "", "ada", "member"); response.Code != http.StatusConflict ||
				!strings.Contains(response.Body.String(), "is already applied") {
				t.Fatalf("a plan applied twice: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestARejectedPlanNeverRuns(t *testing.T) {
	author := &model{program: `codemode.execute("tasks.delete", { id: codemode.execute("tasks.list", {}).tasks[0].id }); return "ok";`}
	handler := newServer(t, author, fromGo(t))
	before := len(adasTasks(t, handler))

	held := decode[chatPending](t, do(t, handler, "POST", "/api/chat", `{"request":"delete one"}`, "ada", "member")).Pending
	if held == nil {
		t.Fatal("nothing was held")
	}
	if response := do(t, handler, "POST", "/api/plans/"+held.ID+"/reject", "", "ada", "member"); response.Code != http.StatusOK {
		t.Fatalf("reject returned %d: %s", response.Code, response.Body)
	}
	if response := do(t, handler, "POST", "/api/plans/"+held.ID+"/approve", "", "ada", "member"); response.Code != http.StatusConflict {
		t.Fatalf("a rejected plan was approved: %d", response.Code)
	}
	if len(adasTasks(t, handler)) != before {
		t.Fatal("a rejected delete ran")
	}
}

func TestTheInboxListsWhatWaitsAndWhatHappened(t *testing.T) {
	author := &model{program: `codemode.execute("tasks.delete", { id: codemode.execute("tasks.list", {}).tasks[0].id }); return "ok";`}
	handler := newServer(t, author, fromGo(t))
	first := decode[chatPending](t, do(t, handler, "POST", "/api/chat", `{"request":"delete one"}`, "ada", "member")).Pending
	second := decode[chatPending](t, do(t, handler, "POST", "/api/chat", `{"request":"delete another"}`, "ada", "member")).Pending
	do(t, handler, "POST", "/api/plans/"+first.ID+"/reject", "", "ada", "member")

	type inbox struct {
		Plans []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			Request   string `json:"request"`
			ExpiresAt string `json:"expires_at"`
			History   []struct {
				Status string `json:"status"`
			} `json:"history"`
		} `json:"plans"`
	}
	waiting := decode[inbox](t, do(t, handler, "GET", "/api/plans?status=pending", "", "ada", "member")).Plans
	if len(waiting) != 1 || waiting[0].ID != second.ID || waiting[0].Request != "delete another" || waiting[0].ExpiresAt == "" {
		t.Fatalf("pending = %+v", waiting)
	}
	all := decode[inbox](t, do(t, handler, "GET", "/api/plans", "", "ada", "member")).Plans
	if len(all) != 2 || all[1].Status != "rejected" || len(all[1].History) != 2 {
		t.Fatalf("all = %+v", all)
	}
	if others := decode[inbox](t, do(t, handler, "GET", "/api/plans", "", "linus", "member")).Plans; len(others) != 0 {
		t.Fatalf("linus sees ada's plans: %+v", others)
	}
}

func TestTheBoardDeleteButtonIsItsOwnApproval(t *testing.T) {
	handler := newServer(t, nil, fromGo(t))
	tasks := adasTasks(t, handler)
	response := do(t, handler, "DELETE", "/api/tasks/"+strconv.FormatInt(tasks[0].ID, 10), "", "ada", "member")
	if response.Code != http.StatusOK || len(adasTasks(t, handler)) != len(tasks)-1 {
		t.Fatalf("board delete returned %d: %s", response.Code, response.Body)
	}
}
