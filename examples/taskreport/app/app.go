// Package app dispatches registry-resolved RPC paths to the store, scoping every call to the caller's identity.
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

// New returns an App; admins names the workspace administrators.
func New(store *store.Store, admins ...string) *App {
	set := make(map[string]bool, len(admins))
	for _, admin := range admins {
		set[admin] = true
	}
	return &App{store: store, admins: set}
}

// RPC paths this application serves, as constants so proto renames surface at compile time.
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

	// No identity is a wiring failure: stop rather than pick a default.
	identity, err := auth.From(ctx)
	if err != nil {
		return nil, err
	}
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

	// Administrators only: members' registries omit these; the checks fail closed on a wiring bug.

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

// members lists the workspace: task owners plus the configured admins.
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

// decode rejects unknown fields so a model learns of invented fields on the first call.
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
