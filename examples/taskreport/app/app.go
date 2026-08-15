// Package app is the trust boundary: it turns an RPC path the registry
// resolved into a call on the application's own store.
//
// Everything that arrives here has already been checked against the registry,
// so the path is one this application published. Nothing else is assumed: the
// JSON is decoded strictly, the store validates its own arguments, and an
// unknown path is an error rather than a fallthrough.
//
// # Who is asking
//
// The identity comes from the context, and every branch derives a store.Scope
// from it before touching a row. No request message carries a user, because a
// field a program can set is a field it can set to someone else's name.
//
// There are two layers here and they do different jobs:
//
//   - The registry decides which capabilities exist for this caller. main.go
//     builds a narrower one for a member, so tasks.assign is not merely hidden
//     from the model — it is absent from the map Execute resolves against.
//   - The scope decides which rows those capabilities may touch. That cannot
//     live in the registry, which knows the capability being called and nothing
//     about the data behind it.
//
// The admin checks below are the second lock on a door the registry already
// locked. They stay because the registry is assembled per request from a role,
// and a bug in that assembly should fail closed.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mukezhz/codemode/examples/taskreport/auth"
	"github.com/mukezhz/codemode/examples/taskreport/store"
)

// App implements codemode.ExecuteFunc over a store.
type App struct {
	store  *store.Store
	admins map[string]bool
}

// New returns an App. Admins names the workspace administrators, which a real
// application would read from its own user table.
func New(store *store.Store, admins ...string) *App {
	set := make(map[string]bool, len(admins))
	for _, admin := range admins {
		set[admin] = true
	}
	return &App{store: store, admins: set}
}

// RPC paths this application serves. Listing them as constants rather than
// string literals in the switch means a rename in the proto surfaces as a
// compile error somewhere, not as a capability that silently stops working.
const (
	taskCreate    = "task.v1.TaskService/Create"
	taskGet       = "task.v1.TaskService/Get"
	taskList      = "task.v1.TaskService/List"
	taskUpdate    = "task.v1.TaskService/Update"
	taskComplete  = "task.v1.TaskService/Complete"
	taskReopen    = "task.v1.TaskService/Reopen"
	taskDelete    = "task.v1.TaskService/Delete"
	taskAddTag    = "task.v1.TaskService/AddTag"
	taskRemoveTag = "task.v1.TaskService/RemoveTag"
	taskSearch    = "task.v1.TaskService/Search"
	taskListFor   = "task.v1.TaskService/ListFor"
	taskAssign    = "task.v1.TaskService/Assign"

	projectList   = "task.v1.ProjectService/List"
	projectRename = "task.v1.ProjectService/Rename"

	reportStatus    = "task.v1.ReportService/StatusCounts"
	reportByProject = "task.v1.ReportService/ByProject"
	reportByPrio    = "task.v1.ReportService/ByPriority"
	reportOverdue   = "task.v1.ReportService/Overdue"
	reportCompleted = "task.v1.ReportService/CompletedBetween"
	reportThrough   = "task.v1.ReportService/Throughput"
	reportByOwner   = "task.v1.ReportService/ByOwner"

	userMe   = "user.v1.UserService/Me"
	userList = "user.v1.UserService/List"
)

// Execute dispatches one capability call. Its signature is codemode.ExecuteFunc.
func (app *App) Execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// A request with no identity is a wiring failure, and the safe response to
	// wiring failure is to stop rather than to pick a default.
	identity, err := auth.From(ctx)
	if err != nil {
		return nil, err
	}
	// An administrator reads the workspace; everyone else reads their own rows.
	scope := store.Only(identity.User)
	if identity.Admin() {
		scope = store.Everyone()
	}

	switch rpc {
	case taskCreate:
		var request struct {
			Title    string   `json:"title"`
			Project  string   `json:"project"`
			Priority string   `json:"priority"`
			DueDate  string   `json:"due_date"`
			Tags     []string `json:"tags"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		// Owner comes from the identity, never from the request.
		return task(app.store.Create(identity.User, request.Title, request.Project, request.Priority, request.DueDate, request.Tags))

	case taskGet:
		id, err := decodeID(input)
		if err != nil {
			return nil, err
		}
		return task(app.store.Get(scope, id))

	case taskList:
		var request struct {
			Project  string `json:"project"`
			Status   string `json:"status"`
			Priority string `json:"priority"`
			Tag      string `json:"tag"`
			Limit    int    `json:"limit"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return tasks(app.store.List(scope, store.Filter{
			Project:  request.Project,
			Status:   request.Status,
			Priority: request.Priority,
			Tag:      request.Tag,
			Limit:    request.Limit,
		}))

	case taskUpdate:
		var request struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Project  string `json:"project"`
			Priority string `json:"priority"`
			DueDate  string `json:"due_date"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return task(app.store.Update(scope, request.ID, request.Title, request.Project, request.Priority, request.DueDate))

	case taskComplete:
		id, err := decodeID(input)
		if err != nil {
			return nil, err
		}
		return task(app.store.Complete(scope, id))

	case taskReopen:
		id, err := decodeID(input)
		if err != nil {
			return nil, err
		}
		return task(app.store.Reopen(scope, id))

	case taskDelete:
		id, err := decodeID(input)
		if err != nil {
			return nil, err
		}
		if err := app.store.Delete(scope, id); err != nil {
			return nil, err
		}
		return encode(map[string]any{"deleted": true})

	case taskAddTag, taskRemoveTag:
		var request struct {
			ID  int64  `json:"id"`
			Tag string `json:"tag"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		if rpc == taskAddTag {
			return task(app.store.AddTag(scope, request.ID, request.Tag))
		}
		return task(app.store.RemoveTag(scope, request.ID, request.Tag))

	case taskSearch:
		var request struct {
			Text  string `json:"text"`
			Limit int    `json:"limit"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return tasks(app.store.Search(scope, request.Text, request.Limit))

	case projectList:
		if err := decode(input, &struct{}{}); err != nil {
			return nil, err
		}
		projects, err := app.store.Projects(scope)
		if err != nil {
			return nil, err
		}
		return encode(map[string]any{"projects": projects})

	case projectRename:
		var request struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		moved, err := app.store.RenameProject(scope, request.From, request.To)
		if err != nil {
			return nil, err
		}
		return encode(map[string]any{"moved": moved})

	case reportStatus:
		if err := decode(input, &struct{}{}); err != nil {
			return nil, err
		}
		counts, err := app.store.StatusCounts(scope)
		if err != nil {
			return nil, err
		}
		return encode(counts)

	case reportByProject, reportByPrio:
		if err := decode(input, &struct{}{}); err != nil {
			return nil, err
		}
		groups, err := app.store.ByProject(scope)
		if rpc == reportByPrio {
			groups, err = app.store.ByPriority(scope)
		}
		if err != nil {
			return nil, err
		}
		return encode(map[string]any{"groups": groups})

	case reportOverdue:
		var request struct {
			AsOf string `json:"as_of"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return tasks(app.store.Overdue(scope, request.AsOf))

	case reportCompleted:
		var request struct {
			Start string `json:"start"`
			End   string `json:"end"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return tasks(app.store.CompletedBetween(scope, request.Start, request.End))

	case reportThrough:
		var request struct {
			Days int `json:"days"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		days, err := app.store.Throughput(scope, request.Days)
		if err != nil {
			return nil, err
		}
		return encode(map[string]any{"days": days})

	case userMe:
		if err := decode(input, &struct{}{}); err != nil {
			return nil, err
		}
		return encode(map[string]any{"user": user{Name: identity.User, Role: string(identity.Role)}})

	// ------------------------------------------------ administrators only
	//
	// The registry handed to a member does not contain these names, so reaching
	// here already means either an administrator or a wiring bug. The checks
	// make the second case fail closed.

	case userList:
		if _, err := auth.RequireAdmin(ctx, "users.list"); err != nil {
			return nil, err
		}
		if err := decode(input, &struct{}{}); err != nil {
			return nil, err
		}
		return encode(map[string]any{"users": app.members()})

	case taskListFor:
		if _, err := auth.RequireAdmin(ctx, "tasks.listFor"); err != nil {
			return nil, err
		}
		var request struct {
			Owner  string `json:"owner"`
			Status string `json:"status"`
			Limit  int    `json:"limit"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		if strings.TrimSpace(request.Owner) == "" {
			return nil, fmt.Errorf("owner is required")
		}
		return tasks(app.store.List(store.Only(request.Owner), store.Filter{
			Status: request.Status,
			Limit:  request.Limit,
		}))

	case taskAssign:
		if _, err := auth.RequireAdmin(ctx, "tasks.assign"); err != nil {
			return nil, err
		}
		var request struct {
			ID    int64  `json:"id"`
			Owner string `json:"owner"`
		}
		if err := decode(input, &request); err != nil {
			return nil, err
		}
		return task(app.store.Assign(request.ID, request.Owner))

	case reportByOwner:
		if _, err := auth.RequireAdmin(ctx, "reports.byOwner"); err != nil {
			return nil, err
		}
		if err := decode(input, &struct{}{}); err != nil {
			return nil, err
		}
		groups, err := app.store.ByOwner(store.Everyone())
		if err != nil {
			return nil, err
		}
		return encode(map[string]any{"groups": groups})

	default:
		return nil, fmt.Errorf("RPC %q is not implemented", rpc)
	}
}

type user struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// members lists the workspace. A real application reads this from its own user
// table; the demo derives it from who owns tasks plus the configured admin.
func (app *App) members() []user {
	owners, err := app.store.Owners()
	if err != nil {
		return nil
	}
	members := make([]user, 0, len(owners))
	for _, owner := range owners {
		role := string(auth.RoleMember)
		if app.admins[owner] {
			role = string(auth.RoleAdmin)
		}
		members = append(members, user{Name: owner, Role: role})
	}
	return members
}

// task and tasks keep every success path one line, so the switch above reads as
// a routing table rather than as error handling.
func task(item store.Task, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return encode(map[string]any{"task": item})
}

func tasks(items []store.Task, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return encode(map[string]any{"tasks": items, "count": len(items)})
}

func encode(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}

func decodeID(input []byte) (int64, error) {
	var request struct {
		ID int64 `json:"id"`
	}
	if err := decode(input, &request); err != nil {
		return 0, err
	}
	if request.ID == 0 {
		return 0, fmt.Errorf("id is required")
	}
	return request.ID, nil
}

// decode is strict on purpose. A model that invents a field name should be told
// so on the first call, while the error can still be fed back for repair,
// rather than having the value silently dropped and the answer come out wrong.
func decode(input []byte, target any) error {
	if len(bytes.TrimSpace(input)) == 0 {
		input = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode input: unexpected trailing data")
	}
	return nil
}
