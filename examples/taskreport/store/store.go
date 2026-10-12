// Package store is the task application's own database; it validates its own input and knows nothing about models.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Task mirrors the protobuf message; JSON tags are the sandbox wire format.
type Task struct {
	ID            int64    `json:"id"`
	Owner         string   `json:"owner"`
	Title         string   `json:"title"`
	Project       string   `json:"project"`
	Priority      string   `json:"priority"`
	DueDate       string   `json:"due_date"`
	Completed     bool     `json:"completed"`
	CompletedDate string   `json:"completed_date"`
	CreatedDate   string   `json:"created_date"`
	Tags          []string `json:"tags"`
}

// Project is one project name with its task counts.
type Project struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Open      int    `json:"open"`
	Completed int    `json:"completed"`
}

// GroupCount is one row of a grouped report.
type GroupCount struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Open      int    `json:"open"`
	Completed int    `json:"completed"`
}

// DayCount is one day of completion throughput.
type DayCount struct {
	Date      string `json:"date"`
	Completed int    `json:"completed"`
}

// Scope is the set of rows a call may touch; the zero Scope reaches nothing.
type Scope struct {
	// Owner limits the call to one person's tasks.
	Owner string
	// All lifts that limit. Only an administrator's request sets it.
	All bool
}

// Only returns a Scope limited to one owner.
func Only(owner string) Scope { return Scope{Owner: owner} }

// Everyone returns an unlimited Scope.
func Everyone() Scope { return Scope{All: true} }

func (scope Scope) where() (string, []any) {
	if scope.All {
		return "", nil
	}
	// Empty owner on a non-All scope matches nothing, not everything.
	return " AND owner = ?", []any{scope.Owner}
}

// Priorities are the accepted priority values, lowest first.
var Priorities = []string{"low", "medium", "high"}

const defaultPriority = "medium"

// Store is the task database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open creates or opens the database at path, which may be ":memory:".
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	// ponytail: one connection avoids SQLITE_BUSY; raise with WAL for concurrency.
	db.SetMaxOpenConns(1)

	store := &Store{db: db, now: time.Now}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// SetClock overrides the clock.
func (store *Store) SetClock(now func() time.Time) { store.now = now }

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) migrate() error {
	_, err := store.db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			owner          TEXT NOT NULL DEFAULT '',
			title          TEXT NOT NULL,
			project        TEXT NOT NULL DEFAULT '',
			priority       TEXT NOT NULL DEFAULT 'medium',
			due_date       TEXT NOT NULL DEFAULT '',
			completed      INTEGER NOT NULL DEFAULT 0,
			completed_date TEXT NOT NULL DEFAULT '',
			created_date   TEXT NOT NULL,
			tags           TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS tasks_owner    ON tasks(owner);
		CREATE INDEX IF NOT EXISTS tasks_project  ON tasks(project);
		CREATE INDEX IF NOT EXISTS tasks_due      ON tasks(due_date);
		CREATE INDEX IF NOT EXISTS tasks_complete ON tasks(completed, completed_date);
	`)
	if err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

// Today is the store's date, in the format every date field uses.
func (store *Store) Today() string { return store.now().Format(dateLayout) }

const dateLayout = "2006-01-02"

// Create inserts a task owned by owner; title is required and priority defaults to medium.
func (store *Store) Create(owner, title, project, priority, dueDate string, tags []string) (Task, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, errors.New("owner is required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, errors.New("title is required")
	}
	priority, err := normalizePriority(priority)
	if err != nil {
		return Task{}, err
	}
	if err := validateDate("due_date", dueDate); err != nil {
		return Task{}, err
	}

	result, err := store.db.Exec(`
		INSERT INTO tasks(owner, title, project, priority, due_date, created_date, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, owner, title, strings.TrimSpace(project), priority, strings.TrimSpace(dueDate),
		store.Today(), encodeTags(tags))
	if err != nil {
		return Task{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	return store.Get(Only(owner), id)
}

// Get returns one task by ID, within scope.
// Out-of-scope tasks report "not found", not "forbidden", so IDs cannot be probed.
func (store *Store) Get(scope Scope, id int64) (Task, error) {
	predicate, args := scope.where()
	row := store.db.QueryRow(taskColumns+` FROM tasks WHERE id = ?`+predicate,
		append([]any{id}, args...)...)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("task %d not found", id)
	}
	return task, err
}

// Filter narrows a listing. Every field is optional.
type Filter struct {
	Project  string
	Status   string // open | completed
	Priority string
	Tag      string
	Limit    int
}

// List returns tasks matching filter within scope, newest first.
func (store *Store) List(scope Scope, filter Filter) ([]Task, error) {
	predicate, args := scope.where()
	query := taskColumns + ` FROM tasks WHERE 1 = 1` + predicate

	if project := strings.TrimSpace(filter.Project); project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}
	switch strings.ToLower(strings.TrimSpace(filter.Status)) {
	case "open":
		query += ` AND completed = 0`
	case "completed":
		query += ` AND completed = 1`
	case "":
	default:
		return nil, fmt.Errorf("status %q is not open or completed", filter.Status)
	}
	if filter.Priority != "" {
		priority, err := normalizePriority(filter.Priority)
		if err != nil {
			return nil, err
		}
		query += ` AND priority = ?`
		args = append(args, priority)
	}
	if tag := strings.TrimSpace(filter.Tag); tag != "" {
		// Delimiters on both sides stop "ui" from matching "build".
		query += ` AND ',' || tags || ',' LIKE ?`
		args = append(args, "%,"+strings.ToLower(tag)+",%")
	}

	query += ` ORDER BY id DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	return store.query(query, args...)
}

// Search returns tasks whose title contains text, case-insensitively.
func (store *Store) Search(scope Scope, text string, limit int) ([]Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("search text is required")
	}
	predicate, scopeArgs := scope.where()
	query := taskColumns + ` FROM tasks WHERE LOWER(title) LIKE ?` + predicate + ` ORDER BY id DESC`
	args := append([]any{"%" + strings.ToLower(text) + "%"}, scopeArgs...)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return store.query(query, args...)
}

// Update writes only the fields that are set.
func (store *Store) Update(scope Scope, id int64, title, project, priority, dueDate string) (Task, error) {
	current, err := store.Get(scope, id)
	if err != nil {
		return Task{}, err
	}
	if title = strings.TrimSpace(title); title != "" {
		current.Title = title
	}
	if project = strings.TrimSpace(project); project != "" {
		current.Project = project
	}
	if priority != "" {
		if current.Priority, err = normalizePriority(priority); err != nil {
			return Task{}, err
		}
	}
	if dueDate = strings.TrimSpace(dueDate); dueDate != "" {
		if err := validateDate("due_date", dueDate); err != nil {
			return Task{}, err
		}
		current.DueDate = dueDate
	}

	if _, err := store.db.Exec(`
		UPDATE tasks SET title = ?, project = ?, priority = ?, due_date = ? WHERE id = ?
	`, current.Title, current.Project, current.Priority, current.DueDate, id); err != nil {
		return Task{}, err
	}
	return store.Get(scope, id)
}

// Complete marks a task completed as of today.
func (store *Store) Complete(scope Scope, id int64) (Task, error) {
	task, err := store.Get(scope, id)
	if err != nil {
		return Task{}, err
	}
	if task.Completed {
		return Task{}, fmt.Errorf("task %d is already completed", id)
	}
	if _, err := store.db.Exec(
		`UPDATE tasks SET completed = 1, completed_date = ? WHERE id = ?`,
		store.Today(), id,
	); err != nil {
		return Task{}, err
	}
	return store.Get(scope, id)
}

// Reopen marks a completed task open again.
func (store *Store) Reopen(scope Scope, id int64) (Task, error) {
	task, err := store.Get(scope, id)
	if err != nil {
		return Task{}, err
	}
	if !task.Completed {
		return Task{}, fmt.Errorf("task %d is already open", id)
	}
	if _, err := store.db.Exec(
		`UPDATE tasks SET completed = 0, completed_date = '' WHERE id = ?`, id,
	); err != nil {
		return Task{}, err
	}
	return store.Get(scope, id)
}

// Delete removes a task permanently.
func (store *Store) Delete(scope Scope, id int64) error {
	predicate, args := scope.where()
	result, err := store.db.Exec(`DELETE FROM tasks WHERE id = ?`+predicate,
		append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("task %d not found", id)
	}
	return nil
}

// AddTag adds a tag, ignoring one the task already has.
func (store *Store) AddTag(scope Scope, id int64, tag string) (Task, error) {
	return store.editTags(scope, id, tag, func(tags []string, tag string) []string {
		for _, existing := range tags {
			if existing == tag {
				return tags
			}
		}
		return append(tags, tag)
	})
}

// RemoveTag removes a tag, ignoring one the task does not have.
func (store *Store) RemoveTag(scope Scope, id int64, tag string) (Task, error) {
	return store.editTags(scope, id, tag, func(tags []string, tag string) []string {
		kept := tags[:0]
		for _, existing := range tags {
			if existing != tag {
				kept = append(kept, existing)
			}
		}
		return kept
	})
}

func (store *Store) editTags(scope Scope, id int64, tag string, edit func([]string, string) []string) (Task, error) {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return Task{}, errors.New("tag is required")
	}
	if strings.Contains(tag, ",") {
		return Task{}, errors.New("a tag may not contain a comma")
	}
	task, err := store.Get(scope, id)
	if err != nil {
		return Task{}, err
	}
	if _, err := store.db.Exec(
		`UPDATE tasks SET tags = ? WHERE id = ?`, encodeTags(edit(task.Tags, tag)), id,
	); err != nil {
		return Task{}, err
	}
	return store.Get(scope, id)
}

// Assign moves a task to a different owner; only administrators reach it.
func (store *Store) Assign(id int64, owner string) (Task, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, errors.New("owner is required")
	}
	if _, err := store.Get(Everyone(), id); err != nil {
		return Task{}, err
	}
	if _, err := store.db.Exec(`UPDATE tasks SET owner = ? WHERE id = ?`, owner, id); err != nil {
		return Task{}, err
	}
	return store.Get(Everyone(), id)
}

// Owners lists everyone who owns at least one task.
func (store *Store) Owners() ([]string, error) {
	rows, err := store.db.Query(`SELECT DISTINCT owner FROM tasks WHERE owner != '' ORDER BY owner`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	owners := []string{}
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	return owners, rows.Err()
}

// Projects lists every project holding at least one task.
func (store *Store) Projects(scope Scope) ([]Project, error) {
	predicate, args := scope.where()
	rows, err := store.db.Query(`
		SELECT project,
		       COUNT(*),
		       SUM(CASE WHEN completed = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN completed = 1 THEN 1 ELSE 0 END)
		FROM tasks
		WHERE 1 = 1`+predicate+`
		GROUP BY project
		ORDER BY COUNT(*) DESC, project
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.Name, &project.Total, &project.Open, &project.Completed); err != nil {
			return nil, err
		}
		if project.Name == "" {
			project.Name = "(none)"
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

// RenameProject moves every task from one project to another.
func (store *Store) RenameProject(scope Scope, from, to string) (int, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" {
		return 0, errors.New("both from and to are required")
	}
	predicate, args := scope.where()
	result, err := store.db.Exec(`UPDATE tasks SET project = ? WHERE project = ?`+predicate,
		append([]any{to, from}, args...)...)
	if err != nil {
		return 0, err
	}
	moved, err := result.RowsAffected()
	return int(moved), err
}

// StatusCounts totals every task by state.
type StatusCounts struct {
	Total     int `json:"total"`
	Open      int `json:"open"`
	Completed int `json:"completed"`
	Overdue   int `json:"overdue"`
	DueToday  int `json:"due_today"`
}

// StatusCounts returns the whole-table totals.
func (store *Store) StatusCounts(scope Scope) (StatusCounts, error) {
	today := store.Today()
	predicate, scopeArgs := scope.where()
	var counts StatusCounts
	// COALESCE: SUM over no rows is NULL.
	err := store.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN completed = 0 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN completed = 1 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN completed = 0 AND due_date != '' AND due_date <  ? THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN completed = 0 AND due_date  = ?                   THEN 1 ELSE 0 END), 0)
		FROM tasks
		WHERE 1 = 1`+predicate+`
	`, append([]any{today, today}, scopeArgs...)...).
		Scan(&counts.Total, &counts.Open, &counts.Completed, &counts.Overdue, &counts.DueToday)
	if err != nil {
		return StatusCounts{}, err
	}
	return counts, nil
}

// ByProject counts open and completed tasks per project.
func (store *Store) ByProject(scope Scope) ([]GroupCount, error) {
	return store.groupCounts(scope, `project`, "(none)")
}

// ByOwner counts open and completed tasks per person.
func (store *Store) ByOwner(scope Scope) ([]GroupCount, error) {
	return store.groupCounts(scope, `owner`, "(unassigned)")
}

// ByPriority counts open and completed tasks per priority, highest first.
func (store *Store) ByPriority(scope Scope) ([]GroupCount, error) {
	groups, err := store.groupCounts(scope, `priority`, defaultPriority)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{}
	for index, priority := range Priorities {
		rank[priority] = index
	}
	sort.SliceStable(groups, func(i, j int) bool { return rank[groups[i].Name] > rank[groups[j].Name] })
	return groups, nil
}

func (store *Store) groupCounts(scope Scope, column, emptyLabel string) ([]GroupCount, error) {
	predicate, args := scope.where()
	rows, err := store.db.Query(`
		SELECT `+column+`,
		       COUNT(*),
		       SUM(CASE WHEN completed = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN completed = 1 THEN 1 ELSE 0 END)
		FROM tasks
		WHERE 1 = 1`+predicate+`
		GROUP BY `+column+`
		ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := []GroupCount{}
	for rows.Next() {
		var group GroupCount
		if err := rows.Scan(&group.Name, &group.Total, &group.Open, &group.Completed); err != nil {
			return nil, err
		}
		if group.Name == "" {
			group.Name = emptyLabel
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// Overdue lists open tasks due before asOf (default today), oldest first.
func (store *Store) Overdue(scope Scope, asOf string) ([]Task, error) {
	if asOf = strings.TrimSpace(asOf); asOf == "" {
		asOf = store.Today()
	} else if err := validateDate("as_of", asOf); err != nil {
		return nil, err
	}
	predicate, args := scope.where()
	return store.query(taskColumns+`
		FROM tasks
		WHERE completed = 0 AND due_date != '' AND due_date < ?`+predicate+`
		ORDER BY due_date ASC`, append([]any{asOf}, args...)...)
}

// CompletedBetween lists tasks completed in a date range, inclusive.
func (store *Store) CompletedBetween(scope Scope, start, end string) ([]Task, error) {
	if err := validateDate("start", start); err != nil {
		return nil, err
	}
	if err := validateDate("end", end); err != nil {
		return nil, err
	}
	if start == "" || end == "" {
		return nil, errors.New("both start and end are required")
	}
	if start > end {
		return nil, fmt.Errorf("start %s is after end %s", start, end)
	}
	predicate, args := scope.where()
	return store.query(taskColumns+`
		FROM tasks
		WHERE completed = 1 AND completed_date BETWEEN ? AND ?`+predicate+`
		ORDER BY completed_date DESC, id DESC`, append([]any{start, end}, args...)...)
}

// Throughput counts completions on each of the last days, most recent first, including empty days.
func (store *Store) Throughput(scope Scope, days int) ([]DayCount, error) {
	if days <= 0 {
		days = 7
	}
	if days > 366 {
		return nil, fmt.Errorf("days must be 366 or fewer, got %d", days)
	}

	today := store.now()
	start := today.AddDate(0, 0, -(days - 1)).Format(dateLayout)

	predicate, scopeArgs := scope.where()
	rows, err := store.db.Query(`
		SELECT completed_date, COUNT(*)
		FROM tasks
		WHERE completed = 1 AND completed_date >= ?`+predicate+`
		GROUP BY completed_date
	`, append([]any{start}, scopeArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counted := map[string]int{}
	for rows.Next() {
		var date string
		var count int
		if err := rows.Scan(&date, &count); err != nil {
			return nil, err
		}
		counted[date] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	trend := make([]DayCount, 0, days)
	for offset := 0; offset < days; offset++ {
		date := today.AddDate(0, 0, -offset).Format(dateLayout)
		trend = append(trend, DayCount{Date: date, Completed: counted[date]})
	}
	return trend, nil
}

const taskColumns = `SELECT id, owner, title, project, priority, due_date, completed, completed_date, created_date, tags`

func (store *Store) query(query string, args ...any) ([]Task, error) {
	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanTask(row scanner) (Task, error) {
	var task Task
	var tags string
	err := row.Scan(&task.ID, &task.Owner, &task.Title, &task.Project, &task.Priority,
		&task.DueDate, &task.Completed, &task.CompletedDate, &task.CreatedDate, &tags)
	if err != nil {
		return Task{}, err
	}
	task.Tags = decodeTags(tags)
	return task, nil
}

func normalizePriority(priority string) (string, error) {
	priority = strings.ToLower(strings.TrimSpace(priority))
	if priority == "" {
		return defaultPriority, nil
	}
	for _, allowed := range Priorities {
		if priority == allowed {
			return priority, nil
		}
	}
	return "", fmt.Errorf("priority %q is not one of %s", priority, strings.Join(Priorities, ", "))
}

// validateDate accepts empty or YYYY-MM-DD.
func validateDate(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if _, err := time.Parse(dateLayout, value); err != nil {
		return fmt.Errorf("%s must be YYYY-MM-DD, got %q", field, value)
	}
	return nil
}

// ponytail: tags are a comma-delimited string; move to a table if filtering needs an index.
func encodeTags(tags []string) string {
	cleaned := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || strings.Contains(tag, ",") || seen[tag] {
			continue
		}
		seen[tag] = true
		cleaned = append(cleaned, tag)
	}
	sort.Strings(cleaned)
	return strings.Join(cleaned, ",")
}

func decodeTags(tags string) []string {
	if tags == "" {
		return []string{}
	}
	return strings.Split(tags, ",")
}
