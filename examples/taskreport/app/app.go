package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mukezhz/codemode/capability/gosource"
	"github.com/mukezhz/codemode/codemode"

	"github.com/mukezhz/codemode/examples/taskreport/auth"
	"github.com/mukezhz/codemode/examples/taskreport/store"
)

type App struct {
	store  *store.Store
	admins map[string]bool
}

func New(store *store.Store, admins ...string) *App {
	set := make(map[string]bool, len(admins))
	for _, admin := range admins {
		set[admin] = true
	}
	return &App{store: store, admins: set}
}

// Source serves every capability from the typed methods below: the request and
// response structs are the schema, and access is declared beside each one.
// Every method is scoped to the signed-in user, and none takes a user ID to say
// who that is; the four that reach across owners are administrators only.
func (app *App) Source() (codemode.Source, error) {
	public, admin, read := gosource.Public(), gosource.Roles(string(auth.RoleAdmin)), gosource.ReadOnly()
	return gosource.New(
		gosource.Func("tasks.create", "Creates a task. Title is required. Priority is low, medium, or high and defaults to medium. Due date is YYYY-MM-DD.", public, app.Create),
		gosource.Func("tasks.get", "Returns one task by its numeric ID.", public, app.Get, read),
		gosource.Func("tasks.list", "Lists tasks, newest first. Every filter is optional and they combine: status is open or completed, and tag matches a single tag.", public, app.List, read),
		gosource.Func("tasks.update", "Changes a task's title, project, priority, or due date. Only the fields you set are written; the rest keep their current values.", public, app.Update),
		gosource.Func("tasks.complete", "Marks a task completed, recording the completion date. Completing an already completed task is an error.", public, app.Complete),
		gosource.Func("tasks.reopen", "Marks a completed task open again and clears its completion date.", public, app.Reopen),
		gosource.Func("tasks.delete", "Deletes a task permanently by ID. The user approves each deletion before it happens.", public, app.Delete, gosource.NeedsApproval()),
		gosource.Func("tasks.addTag", "Adds a tag to a task. Adding a tag it already has changes nothing.", public, app.AddTag),
		gosource.Func("tasks.removeTag", "Removes a tag from a task.", public, app.RemoveTag),
		gosource.Func("tasks.search", "Finds tasks whose title contains the given text, case-insensitively.", public, app.Search, read),
		gosource.Func("tasks.listFor", "Lists another user's tasks. Administrators only; everyone else uses tasks.list, which returns their own.", admin, app.ListFor, read),
		gosource.Func("tasks.assign", "Moves a task to a different owner. Administrators only.", admin, app.Assign),

		gosource.Func("projects.list", "Lists every project name that has at least one task, with how many tasks each holds.", public, app.Projects, read),
		gosource.Func("projects.rename", "Moves every task in one project to another project name.", public, app.RenameProject),

		gosource.Func("reports.statusCounts", "Totals every task by state: open, completed, overdue, and due today.", public, app.StatusCounts, read),
		gosource.Func("reports.byProject", "Counts open and completed tasks per project, so one call answers where the work sits.", public, app.ByProject, read),
		gosource.Func("reports.byPriority", "Counts open and completed tasks per priority level.", public, app.ByPriority, read),
		gosource.Func("reports.overdue", "Lists open tasks whose due date is before as_of, oldest first. Leave as_of empty for today.", public, app.Overdue, read),
		gosource.Func("reports.completedBetween", "Lists tasks completed between two YYYY-MM-DD dates, inclusive.", public, app.CompletedBetween, read),
		gosource.Func("reports.throughput", "Counts tasks completed on each of the last N days, most recent first, including days with none.", public, app.Throughput, read),
		gosource.Func("reports.byOwner", "Counts open and completed tasks per person across the whole workspace. Administrators only.", admin, app.ByOwner, read),

		gosource.Func("users.me", "Returns the signed-in user: their name and role. Takes no arguments — the caller is already known.", public, app.Me, read),
		gosource.Func("users.list", "Lists everyone in the workspace with their role. Administrators only.", admin, app.Users, read),
	)
}

type Priority string

func (Priority) Enum() []string { return []string{"low", "medium", "high"} }

type Status string

func (Status) Enum() []string { return []string{"open", "completed"} }

type CreateTask struct {
	Title    string   `json:"title"`
	Project  string   `json:"project,omitempty"`
	Priority Priority `json:"priority,omitempty" desc:"defaults to medium"`
	DueDate  string   `json:"due_date,omitempty" desc:"YYYY-MM-DD"`
	Tags     []string `json:"tags,omitempty"`
}

type TaskID struct {
	ID int64 `json:"id"`
}

type ListTasks struct {
	Project  string   `json:"project,omitempty"`
	Status   Status   `json:"status,omitempty" desc:"leave out for both"`
	Priority Priority `json:"priority,omitempty"`
	Tag      string   `json:"tag,omitempty"`
	Limit    int      `json:"limit,omitempty" desc:"leave out for every match"`
}

type UpdateTask struct {
	ID       int64    `json:"id"`
	Title    string   `json:"title,omitempty"`
	Project  string   `json:"project,omitempty"`
	Priority Priority `json:"priority,omitempty"`
	DueDate  string   `json:"due_date,omitempty" desc:"YYYY-MM-DD"`
}

type TagTask struct {
	ID  int64  `json:"id"`
	Tag string `json:"tag"`
}

type SearchTasks struct {
	Text  string `json:"text"`
	Limit int    `json:"limit,omitempty"`
}

type ListFor struct {
	Owner  string `json:"owner"`
	Status Status `json:"status,omitempty" desc:"leave out for both"`
	Limit  int    `json:"limit,omitempty"`
}

type AssignTask struct {
	ID    int64  `json:"id"`
	Owner string `json:"owner"`
}

type RenameProject struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type AsOf struct {
	AsOf string `json:"as_of,omitempty" desc:"YYYY-MM-DD; leave out for today"`
}

type DateRange struct {
	Start string `json:"start" desc:"YYYY-MM-DD"`
	End   string `json:"end" desc:"YYYY-MM-DD"`
}

type Days struct {
	Days int `json:"days"`
}

type None struct{}

type TaskResult struct {
	Task store.Task `json:"task"`
}

type TaskList struct {
	Tasks []store.Task `json:"tasks"`
	Count int          `json:"count"`
}

type Deleted struct {
	Deleted bool `json:"deleted"`
}

type ProjectList struct {
	Projects []store.Project `json:"projects"`
}

type Moved struct {
	Moved int `json:"moved"`
}

type Groups struct {
	Groups []store.GroupCount `json:"groups"`
}

type Throughput struct {
	Days []store.DayCount `json:"days"`
}

type User struct {
	Name string `json:"name"`
	Role string `json:"role" desc:"member or admin"`
}

type UserResult struct {
	User User `json:"user"`
}

type UserList struct {
	Users []User `json:"users"`
}

// No identity is a wiring failure: stop rather than pick a default.
func scope(ctx context.Context) (auth.Identity, store.Scope, error) {
	if err := ctx.Err(); err != nil {
		return auth.Identity{}, store.Scope{}, err
	}
	identity, err := auth.From(ctx)
	if err != nil {
		return auth.Identity{}, store.Scope{}, err
	}
	if identity.Admin() {
		return identity, store.Everyone(), nil
	}
	return identity, store.Only(identity.User), nil
}

func (app *App) Create(ctx context.Context, request CreateTask) (TaskResult, error) {
	identity, _, err := scope(ctx)
	if err != nil {
		return TaskResult{}, err
	}
	// Owner comes from the identity, never from the request.
	return task(app.store.Create(identity.User, request.Title, request.Project, string(request.Priority), request.DueDate, request.Tags))
}

func (app *App) Get(ctx context.Context, request TaskID) (TaskResult, error) {
	_, scope, err := withID(ctx, request.ID)
	if err != nil {
		return TaskResult{}, err
	}
	return task(app.store.Get(scope, request.ID))
}

func (app *App) List(ctx context.Context, request ListTasks) (TaskList, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskList{}, err
	}
	return tasks(app.store.List(scope, store.Filter{
		Project: request.Project, Status: string(request.Status), Priority: string(request.Priority),
		Tag: request.Tag, Limit: request.Limit,
	}))
}

func (app *App) Update(ctx context.Context, request UpdateTask) (TaskResult, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskResult{}, err
	}
	return task(app.store.Update(scope, request.ID, request.Title, request.Project, string(request.Priority), request.DueDate))
}

func (app *App) Complete(ctx context.Context, request TaskID) (TaskResult, error) {
	_, scope, err := withID(ctx, request.ID)
	if err != nil {
		return TaskResult{}, err
	}
	return task(app.store.Complete(scope, request.ID))
}

func (app *App) Reopen(ctx context.Context, request TaskID) (TaskResult, error) {
	_, scope, err := withID(ctx, request.ID)
	if err != nil {
		return TaskResult{}, err
	}
	return task(app.store.Reopen(scope, request.ID))
}

func (app *App) Delete(ctx context.Context, request TaskID) (Deleted, error) {
	_, scope, err := withID(ctx, request.ID)
	if err != nil {
		return Deleted{}, err
	}
	if err := app.store.Delete(scope, request.ID); err != nil {
		return Deleted{}, err
	}
	return Deleted{Deleted: true}, nil
}

func (app *App) AddTag(ctx context.Context, request TagTask) (TaskResult, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskResult{}, err
	}
	return task(app.store.AddTag(scope, request.ID, request.Tag))
}

func (app *App) RemoveTag(ctx context.Context, request TagTask) (TaskResult, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskResult{}, err
	}
	return task(app.store.RemoveTag(scope, request.ID, request.Tag))
}

func (app *App) Search(ctx context.Context, request SearchTasks) (TaskList, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskList{}, err
	}
	return tasks(app.store.Search(scope, request.Text, request.Limit))
}

func (app *App) Projects(ctx context.Context, _ None) (ProjectList, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return ProjectList{}, err
	}
	projects, err := app.store.Projects(scope)
	return ProjectList{Projects: orEmpty(projects)}, err
}

func (app *App) RenameProject(ctx context.Context, request RenameProject) (Moved, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return Moved{}, err
	}
	moved, err := app.store.RenameProject(scope, request.From, request.To)
	return Moved{Moved: moved}, err
}

func (app *App) StatusCounts(ctx context.Context, _ None) (store.StatusCounts, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return store.StatusCounts{}, err
	}
	return app.store.StatusCounts(scope)
}

func (app *App) ByProject(ctx context.Context, _ None) (Groups, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return Groups{}, err
	}
	return groups(app.store.ByProject(scope))
}

func (app *App) ByPriority(ctx context.Context, _ None) (Groups, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return Groups{}, err
	}
	return groups(app.store.ByPriority(scope))
}

func (app *App) Overdue(ctx context.Context, request AsOf) (TaskList, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskList{}, err
	}
	return tasks(app.store.Overdue(scope, request.AsOf))
}

func (app *App) CompletedBetween(ctx context.Context, request DateRange) (TaskList, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return TaskList{}, err
	}
	return tasks(app.store.CompletedBetween(scope, request.Start, request.End))
}

func (app *App) Throughput(ctx context.Context, request Days) (Throughput, error) {
	_, scope, err := scope(ctx)
	if err != nil {
		return Throughput{}, err
	}
	days, err := app.store.Throughput(scope, request.Days)
	return Throughput{Days: orEmpty(days)}, err
}

func (app *App) Me(ctx context.Context, _ None) (UserResult, error) {
	identity, _, err := scope(ctx)
	if err != nil {
		return UserResult{}, err
	}
	return UserResult{User: User{Name: identity.User, Role: string(identity.Role)}}, nil
}

// Administrators only: members' registries omit these, and the checks fail closed on a wiring bug.

func (app *App) Users(ctx context.Context, _ None) (UserList, error) {
	if _, err := auth.RequireAdmin(ctx, "users.list"); err != nil {
		return UserList{}, err
	}
	return UserList{Users: app.members()}, nil
}

func (app *App) ListFor(ctx context.Context, request ListFor) (TaskList, error) {
	if _, err := auth.RequireAdmin(ctx, "tasks.listFor"); err != nil {
		return TaskList{}, err
	}
	if strings.TrimSpace(request.Owner) == "" {
		return TaskList{}, errors.New("owner is required")
	}
	return tasks(app.store.List(store.Only(request.Owner), store.Filter{Status: string(request.Status), Limit: request.Limit}))
}

func (app *App) Assign(ctx context.Context, request AssignTask) (TaskResult, error) {
	if _, err := auth.RequireAdmin(ctx, "tasks.assign"); err != nil {
		return TaskResult{}, err
	}
	return task(app.store.Assign(request.ID, request.Owner))
}

func (app *App) ByOwner(ctx context.Context, _ None) (Groups, error) {
	if _, err := auth.RequireAdmin(ctx, "reports.byOwner"); err != nil {
		return Groups{}, err
	}
	return groups(app.store.ByOwner(store.Everyone()))
}

func (app *App) members() []User {
	owners, err := app.store.Owners()
	if err != nil {
		return []User{}
	}
	members := make([]User, 0, len(owners))
	for _, owner := range owners {
		role := string(auth.RoleMember)
		if app.admins[owner] {
			role = string(auth.RoleAdmin)
		}
		members = append(members, User{Name: owner, Role: role})
	}
	return members
}

func withID(ctx context.Context, id int64) (auth.Identity, store.Scope, error) {
	if id == 0 {
		return auth.Identity{}, store.Scope{}, errors.New("id is required")
	}
	return scope(ctx)
}

func task(item store.Task, err error) (TaskResult, error) {
	return TaskResult{Task: item}, err
}

func tasks(items []store.Task, err error) (TaskList, error) {
	return TaskList{Tasks: orEmpty(items), Count: len(items)}, err
}

func groups(items []store.GroupCount, err error) (Groups, error) {
	return Groups{Groups: orEmpty(items)}, err
}

// The proto path encodes with encoding/json, so a nil list would reach the program as null.
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// Execute serves the proto registry's RPC paths with the same typed methods.
// ponytail: hand-written routing until registry.Source dispatches generated services.
func (app *App) Execute(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
	handler, ok := app.protoRoutes()[rpc]
	if !ok {
		return nil, fmt.Errorf("RPC %q is not implemented", rpc)
	}
	return handler(ctx, input)
}

type route func(context.Context, json.RawMessage) (json.RawMessage, error)

func (app *App) protoRoutes() map[string]route {
	return map[string]route{
		"task.v1.TaskService/Create":             serve(app.Create),
		"task.v1.TaskService/Get":                serve(app.Get),
		"task.v1.TaskService/List":               serve(app.List),
		"task.v1.TaskService/Update":             serve(app.Update),
		"task.v1.TaskService/Complete":           serve(app.Complete),
		"task.v1.TaskService/Reopen":             serve(app.Reopen),
		"task.v1.TaskService/Delete":             serve(app.Delete),
		"task.v1.TaskService/AddTag":             serve(app.AddTag),
		"task.v1.TaskService/RemoveTag":          serve(app.RemoveTag),
		"task.v1.TaskService/Search":             serve(app.Search),
		"task.v1.TaskService/ListFor":            serve(app.ListFor),
		"task.v1.TaskService/Assign":             serve(app.Assign),
		"task.v1.ProjectService/List":            serve(app.Projects),
		"task.v1.ProjectService/Rename":          serve(app.RenameProject),
		"task.v1.ReportService/StatusCounts":     serve(app.StatusCounts),
		"task.v1.ReportService/ByProject":        serve(app.ByProject),
		"task.v1.ReportService/ByPriority":       serve(app.ByPriority),
		"task.v1.ReportService/Overdue":          serve(app.Overdue),
		"task.v1.ReportService/CompletedBetween": serve(app.CompletedBetween),
		"task.v1.ReportService/Throughput":       serve(app.Throughput),
		"task.v1.ReportService/ByOwner":          serve(app.ByOwner),
		"user.v1.UserService/Me":                 serve(app.Me),
		"user.v1.UserService/List":               serve(app.Users),
	}
}

// decode rejects unknown fields so a model learns of invented fields on the first call.
func serve[In, Out any](handler func(context.Context, In) (Out, error)) route {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		if len(bytes.TrimSpace(input)) == 0 {
			input = []byte("{}")
		}
		var request In
		decoder := json.NewDecoder(bytes.NewReader(input))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, errors.New("decode input: unexpected trailing data")
		}
		response, err := handler(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(response)
	}
}
