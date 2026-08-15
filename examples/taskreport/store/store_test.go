package store_test

import (
	"testing"
	"time"

	"github.com/mukezhz/codemode/examples/taskreport/store"
)

// fixed is the clock every test runs against. Reports that say "overdue" or
// "today" are only testable if today is not the wall clock.
var fixed = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) *store.Store {
	t.Helper()
	tasks, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	tasks.SetClock(func() time.Time { return fixed })
	t.Cleanup(func() { tasks.Close() })
	return tasks
}

func TestCreateAppliesDefaultsAndValidates(t *testing.T) {
	tasks := open(t)

	task, err := tasks.Create("ada", "Write the guide", "docs", "", "2026-08-20", []string{"Writing", "writing", " "})
	if err != nil {
		t.Fatal(err)
	}
	if task.Priority != "medium" {
		t.Errorf("priority = %q, want the medium default", task.Priority)
	}
	if task.CreatedDate != "2026-08-15" {
		t.Errorf("created_date = %q, want the store's clock", task.CreatedDate)
	}
	// Tags are lower-cased and deduplicated, so a filter cannot miss one.
	if len(task.Tags) != 1 || task.Tags[0] != "writing" {
		t.Errorf("tags = %v, want [writing]", task.Tags)
	}

	if _, err := tasks.Create("ada", "  ", "", "", "", nil); err == nil {
		t.Error("Create accepted a blank title")
	}
	if _, err := tasks.Create("ada", "x", "", "urgent", "", nil); err == nil {
		t.Error("Create accepted an unknown priority")
	}
	// A malformed date stored as-is would make every range query silently
	// wrong rather than fail.
	if _, err := tasks.Create("ada", "x", "", "", "20th August", nil); err == nil {
		t.Error("Create accepted a malformed due date")
	}
}

func TestUpdateWritesOnlyTheFieldsGiven(t *testing.T) {
	tasks := open(t)
	created, err := tasks.Create("ada", "Original", "docs", "low", "2026-08-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := tasks.Update(store.Everyone(), created.ID, "", "", "high", "")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Priority != "high" {
		t.Errorf("priority = %q, want high", updated.Priority)
	}
	if updated.Title != "Original" || updated.Project != "docs" || updated.DueDate != "2026-08-20" {
		t.Errorf("update cleared fields it was not given: %+v", updated)
	}
}

func TestCompleteAndReopenAreNotIdempotent(t *testing.T) {
	tasks := open(t)
	created, _ := tasks.Create("ada", "Ship it", "platform", "high", "", nil)

	done, err := tasks.Complete(store.Everyone(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !done.Completed || done.CompletedDate != "2026-08-15" {
		t.Fatalf("task = %+v, want completed today", done)
	}
	// Completing twice is a mistake worth surfacing: a program that does it has
	// lost track of state, and a silent success hides that.
	if _, err := tasks.Complete(store.Everyone(), created.ID); err == nil {
		t.Error("Complete accepted an already completed task")
	}

	reopened, err := tasks.Reopen(store.Everyone(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Completed || reopened.CompletedDate != "" {
		t.Fatalf("task = %+v, want open with no completion date", reopened)
	}
	if _, err := tasks.Reopen(store.Everyone(), created.ID); err == nil {
		t.Error("Reopen accepted an already open task")
	}
}

func TestListFiltersCombine(t *testing.T) {
	tasks := open(t)
	first, _ := tasks.Create("ada", "Alpha", "docs", "high", "", []string{"writing"})
	tasks.Create("ada", "Beta", "docs", "low", "", []string{"writing"})
	tasks.Create("ada", "Gamma", "platform", "high", "", []string{"backend"})
	tasks.Complete(store.Everyone(), first.ID)

	found, err := tasks.List(store.Everyone(), store.Filter{Project: "docs", Status: "open", Priority: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Title != "Beta" {
		t.Fatalf("found %v, want only Beta", titles(found))
	}

	if _, err := tasks.List(store.Everyone(), store.Filter{Status: "done"}); err == nil {
		t.Error("List accepted a status that is not open or completed")
	}
}

// Tags are stored delimited in one column, so a substring match would let "ui"
// match "build". The delimiters on both sides are what stops that.
func TestTagFilterDoesNotMatchSubstrings(t *testing.T) {
	tasks := open(t)
	tasks.Create("ada", "Has ui", "design", "", "", []string{"ui"})
	tasks.Create("ada", "Has build", "platform", "", "", []string{"build"})

	found, err := tasks.List(store.Everyone(), store.Filter{Tag: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Title != "Has ui" {
		t.Fatalf("found %v, want only the ui task", titles(found))
	}
}

func TestOverdueUsesTheStoreClock(t *testing.T) {
	tasks := open(t)
	tasks.Create("ada", "Late", "platform", "high", "2026-08-10", nil)
	tasks.Create("ada", "Due today", "platform", "high", "2026-08-15", nil)
	tasks.Create("ada", "Later", "platform", "low", "2026-08-30", nil)
	done, _ := tasks.Create("ada", "Late but done", "platform", "low", "2026-08-01", nil)
	tasks.Complete(store.Everyone(), done.ID)

	overdue, err := tasks.Overdue(store.Everyone(), "")
	if err != nil {
		t.Fatal(err)
	}
	// Due today is not overdue, and a completed task never is.
	if len(overdue) != 1 || overdue[0].Title != "Late" {
		t.Fatalf("overdue = %v, want only Late", titles(overdue))
	}

	asOf, err := tasks.Overdue(store.Everyone(), "2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(asOf) != 3 {
		t.Fatalf("overdue as of September = %v, want 3", titles(asOf))
	}
}

func TestStatusCountsSeparatesOverdueFromDueToday(t *testing.T) {
	tasks := open(t)
	tasks.Create("ada", "Late", "", "", "2026-08-10", nil)
	tasks.Create("ada", "Today", "", "", "2026-08-15", nil)
	tasks.Create("ada", "Undated", "", "", "", nil)
	done, _ := tasks.Create("ada", "Done", "", "", "2026-08-01", nil)
	tasks.Complete(store.Everyone(), done.ID)

	counts, err := tasks.StatusCounts(store.Everyone())
	if err != nil {
		t.Fatal(err)
	}
	want := store.StatusCounts{Total: 4, Open: 3, Completed: 1, Overdue: 1, DueToday: 1}
	if counts != want {
		t.Fatalf("counts = %+v, want %+v", counts, want)
	}
}

// A trend with the quiet days removed is not a trend, so the gaps have to be
// filled by the store rather than reconstructed by every caller.
func TestThroughputIncludesDaysWithNoCompletions(t *testing.T) {
	tasks := open(t)
	done, _ := tasks.Create("ada", "Done today", "", "", "", nil)
	tasks.Complete(store.Everyone(), done.ID)

	trend, err := tasks.Throughput(store.Everyone(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 3 {
		t.Fatalf("trend = %+v, want 3 days", trend)
	}
	if trend[0].Date != "2026-08-15" || trend[0].Completed != 1 {
		t.Errorf("today = %+v, want one completion", trend[0])
	}
	if trend[1].Completed != 0 || trend[2].Completed != 0 {
		t.Errorf("trend = %+v, want the quiet days present and zero", trend)
	}
	if trend[2].Date != "2026-08-13" {
		t.Errorf("oldest day = %q, want 2026-08-13", trend[2].Date)
	}
}

func TestCompletedBetweenRejectsABackwardsRange(t *testing.T) {
	tasks := open(t)
	if _, err := tasks.CompletedBetween(store.Everyone(), "2026-08-20", "2026-08-01"); err == nil {
		t.Error("CompletedBetween accepted a start after its end")
	}
	if _, err := tasks.CompletedBetween(store.Everyone(), "last week", "2026-08-01"); err == nil {
		t.Error("CompletedBetween accepted a malformed date")
	}
}

func TestByPriorityIsOrderedHighestFirst(t *testing.T) {
	tasks := open(t)
	tasks.Create("ada", "a", "", "low", "", nil)
	tasks.Create("ada", "b", "", "high", "", nil)
	tasks.Create("ada", "c", "", "medium", "", nil)

	groups, err := tasks.ByPriority(store.Everyone())
	if err != nil {
		t.Fatal(err)
	}
	// Alphabetical would give high, low, medium — which reads as noise.
	for index, want := range []string{"high", "medium", "low"} {
		if groups[index].Name != want {
			t.Fatalf("groups = %+v, want highest priority first", groups)
		}
	}
}

func TestProjectsAndRename(t *testing.T) {
	tasks := open(t)
	tasks.Create("ada", "a", "docs", "", "", nil)
	tasks.Create("ada", "b", "docs", "", "", nil)
	tasks.Create("ada", "c", "", "", "", nil)

	moved, err := tasks.RenameProject(store.Everyone(), "docs", "documentation")
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Fatalf("moved = %d, want 2", moved)
	}

	projects, err := tasks.Projects(store.Everyone())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, project := range projects {
		names[project.Name] = project.Total
	}
	if names["documentation"] != 2 {
		t.Errorf("projects = %+v, want documentation with 2", projects)
	}
	// A blank project is real data, so it gets a label rather than an empty row.
	if names["(none)"] != 1 {
		t.Errorf("projects = %+v, want the unassigned task labelled", projects)
	}
}

func titles(tasks []store.Task) []string {
	names := make([]string, len(tasks))
	for index, task := range tasks {
		names[index] = task.Title
	}
	return names
}

// Every read takes a Scope, so a code path that forgets to build one gets the
// zero value. That has to reach nothing rather than everything: fail-closed is
// the only safe default for the thing standing between two users' data.
func TestZeroScopeReachesNothing(t *testing.T) {
	tasks := open(t)
	mine, _ := tasks.Create("ada", "Ada's task", "platform", "high", "2026-08-01", nil)
	tasks.Create("linus", "Linus's task", "docs", "low", "", nil)

	var zero store.Scope
	found, err := tasks.List(zero, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("the zero Scope returned %v", titles(found))
	}
	if _, err := tasks.Get(zero, mine.ID); err == nil {
		t.Fatal("the zero Scope read a task")
	}
	counts, err := tasks.StatusCounts(zero)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 0 {
		t.Fatalf("counts = %+v, want everything zero", counts)
	}
}

func TestScopeSeparatesOwners(t *testing.T) {
	tasks := open(t)
	tasks.Create("ada", "Ada's task", "platform", "high", "2026-08-01", nil)
	theirs, _ := tasks.Create("linus", "Linus's task", "docs", "low", "2026-08-01", nil)

	mine, err := tasks.List(store.Only("ada"), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].Title != "Ada's task" {
		t.Fatalf("ada sees %v", titles(mine))
	}

	// Writes are scoped too, or a known ID would be enough to change a row.
	if _, err := tasks.Complete(store.Only("ada"), theirs.ID); err == nil {
		t.Fatal("ada completed a task belonging to linus")
	}
	if err := tasks.Delete(store.Only("ada"), theirs.ID); err == nil {
		t.Fatal("ada deleted a task belonging to linus")
	}

	// Reports respect the scope, or a total leaks what a listing does not.
	overdue, err := tasks.Overdue(store.Only("ada"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(overdue) != 1 {
		t.Fatalf("overdue = %v, want only ada's", titles(overdue))
	}
	everyone, err := tasks.Overdue(store.Everyone(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(everyone) != 2 {
		t.Fatalf("overdue for everyone = %v, want both", titles(everyone))
	}
}
