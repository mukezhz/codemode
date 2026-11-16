package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/agent/plantest"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/goja"
)

func TestMemoryPlanStorePassesTheContract(t *testing.T) {
	plantest.Run(t, func(*testing.T) agent.PlanStore { return agent.NewMemoryPlanStore() })
}

type script []string

func (script *script) Name() string { return "test/script" }

func (script *script) Complete(context.Context, string, string) (string, error) {
	if len(*script) == 0 {
		return "", errors.New("no programs left")
	}
	program := (*script)[0]
	*script = (*script)[1:]
	return program, nil
}

type world struct {
	mode     codemode.CodeMode
	runner   sandbox.Runner
	executed *[]string
	failOn   map[string]bool
}

func newWorld(t *testing.T) *world {
	t.Helper()
	executed := &[]string{}
	world := &world{executed: executed, failOn: map[string]bool{}}
	source := codemode.Source{
		Registry: capability.Registry{Capabilities: []capability.Capability{
			{Name: "tasks.list", RPC: "list", Description: "Lists tasks", ReadOnly: true},
			{Name: "tasks.rename", RPC: "rename", Description: "Renames a task"},
			{Name: "tasks.delete", RPC: "delete", Description: "Deletes a task", NeedsApproval: true},
		}},
		Execute: func(_ context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
			*executed = append(*executed, rpc+string(input))
			if world.failOn[string(input)] {
				return nil, errors.New("task is locked")
			}
			return json.RawMessage(`{"tasks":[{"id":1},{"id":2}]}`), nil
		},
	}
	mode, err := codemode.FromSource(source, capability.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := goja.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Close() })
	world.mode, world.runner = mode, runner
	return world
}

const deleteBoth = `
	const { tasks } = codemode.execute("tasks.list", {});
	for (const task of tasks) codemode.execute("tasks.delete", { id: task.id });
	return "done";`

type clock struct{ now time.Time }

func (clock *clock) Now() time.Time { return clock.now }

func hold(t *testing.T, world *world, plans *agent.Plans, owner string) *agent.Pending {
	t.Helper()
	model := &script{deleteBoth}
	_, err := agent.Run(context.Background(), world.mode, model, "delete both", agent.Options{Runner: world.runner, Plans: plans, PlanOwner: owner})
	var pending *agent.Pending
	if !errors.As(err, &pending) {
		t.Fatalf("err = %v, want *agent.Pending", err)
	}
	return pending
}

func TestAPlanWaitsInTheInboxUntilApproved(t *testing.T) {
	world := newWorld(t)
	var events []agent.Event
	time0 := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{TTL: time.Hour, Now: time0.Now,
		OnEvent: func(event agent.Event) { events = append(events, event) }})

	pending := hold(t, world, plans, "ada")
	if pending.ID == "" || pending.RunID == "" || !pending.ExpiresAt.Equal(time0.now.Add(time.Hour)) || len(pending.Calls) != 2 {
		t.Fatalf("pending = %+v", pending.Plan)
	}
	if strings.Join(*world.executed, ",") != "list{}" {
		t.Fatalf("executed %v before approval", *world.executed)
	}

	inbox, err := plans.List(context.Background(), "ada", agent.PlanPending)
	if err != nil || len(inbox) != 1 || inbox[0].ID != pending.ID || inbox[0].Request != "delete both" {
		t.Fatalf("inbox = %+v, err = %v", inbox, err)
	}

	time0.now = time0.now.Add(10 * time.Minute)
	plan, err := plans.Approve(context.Background(), world.mode, "ada", pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != agent.PlanApplied || len(plan.Applied) != 2 || strings.Join(*world.executed, ",") != `list{},delete{"id":1},delete{"id":2}` {
		t.Fatalf("plan = %+v, executed = %v", plan, *world.executed)
	}
	var path []string
	for _, change := range plan.History {
		path = append(path, string(change.Status))
	}
	if strings.Join(path, ">") != "pending>applying>applied" || plan.History[1].By != "ada" {
		t.Fatalf("history = %+v", plan.History)
	}

	_, err = plans.Approve(context.Background(), world.mode, "ada", pending.ID)
	if !errors.Is(err, agent.ErrPlanDecided) || !strings.Contains(err.Error(), "is already applied (by ada at 2026-11-16T12:10:00Z)") {
		t.Fatalf("second approve: %v", err)
	}
	if _, err := plans.Approve(context.Background(), world.mode, "grace", pending.ID); !errors.Is(err, agent.ErrNoPlan) {
		t.Fatalf("another owner: %v", err)
	}

	var kinds []string
	for _, event := range events {
		kinds = append(kinds, event.Kind)
		if event.PlanID != pending.ID || event.RunID != pending.RunID {
			t.Fatalf("event %+v does not carry the plan and run", event)
		}
	}
	if strings.Join(kinds, ",") != "plan.held,plan.applying,call.done,call.done,plan.applied" {
		t.Fatalf("events = %v", kinds)
	}
}

func TestARejectedPlanNeverRuns(t *testing.T) {
	world := newWorld(t)
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{})
	pending := hold(t, world, plans, "ada")

	plan, err := plans.Reject(context.Background(), "ada", pending.ID, "not today")
	if err != nil || plan.Status != agent.PlanRejected || plan.History[1].Note != "not today" {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
	if _, err := plans.Approve(context.Background(), world.mode, "ada", pending.ID); !errors.Is(err, agent.ErrPlanDecided) {
		t.Fatalf("approved a rejected plan: %v", err)
	}
	if len(*world.executed) != 1 {
		t.Fatalf("executed = %v", *world.executed)
	}
}

func TestAPlanExpiresWhenNobodyDecides(t *testing.T) {
	world := newWorld(t)
	time0 := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	var expired int
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{TTL: time.Hour, Now: time0.Now,
		OnEvent: func(event agent.Event) {
			if event.Kind == agent.EventPlanExpired {
				expired++
			}
		}})
	pending := hold(t, world, plans, "ada")

	time0.now = time0.now.Add(2 * time.Hour)
	listed, err := plans.List(context.Background(), "ada", "")
	if err != nil || len(listed) != 1 || listed[0].Status != agent.PlanExpired {
		t.Fatalf("listed = %+v, err = %v", listed, err)
	}
	_, err = plans.Approve(context.Background(), world.mode, "ada", pending.ID)
	if !errors.Is(err, agent.ErrPlanExpired) || !strings.Contains(err.Error(), "expired at 2026-11-16T13:00:00Z") {
		t.Fatalf("approve after expiry: %v", err)
	}
	if expired != 1 || len(*world.executed) != 1 {
		t.Fatalf("expired events = %d, executed = %v", expired, *world.executed)
	}
}

func TestAFailedCallStopsThePlanAndSaysWhich(t *testing.T) {
	world := newWorld(t)
	world.failOn[`{"id":1}`] = true
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{})
	pending := hold(t, world, plans, "ada")

	plan, err := plans.Approve(context.Background(), world.mode, "ada", pending.ID)
	if err == nil || !strings.Contains(err.Error(), "plan "+pending.ID+": call 1 of 2 (tasks.delete):") || !strings.Contains(err.Error(), "task is locked") {
		t.Fatalf("err = %v", err)
	}
	if plan.Status != agent.PlanFailed || len(plan.Applied) != 1 || plan.Applied[0].Error == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if stored, _ := plans.Get(context.Background(), "ada", pending.ID); stored.Status != agent.PlanFailed {
		t.Fatalf("stored status = %s", stored.Status)
	}
}

func TestAPlanResumesAfterACrash(t *testing.T) {
	world := newWorld(t)
	store := agent.NewMemoryPlanStore()
	plans := agent.NewPlans(store, agent.PlanOptions{})
	pending := hold(t, world, plans, "ada")

	// Simulate a crash after the first delete: applying, one result recorded.
	crashed, _ := store.Get(context.Background(), "ada", pending.ID)
	crashed.Status = agent.PlanApplying
	crashed.Applied = []codemode.Call{{Name: "tasks.delete", Input: crashed.Calls[0].Input}}
	if err := store.Update(context.Background(), &crashed); err != nil {
		t.Fatal(err)
	}

	if _, err := plans.Approve(context.Background(), world.mode, "ada", pending.ID); !errors.Is(err, agent.ErrPlanDecided) {
		t.Fatalf("approve on an applying plan: %v", err)
	}
	plan, err := plans.Resume(context.Background(), world.mode, "ada", pending.ID)
	if err != nil || plan.Status != agent.PlanApplied {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
	if strings.Join(*world.executed, ",") != `list{},delete{"id":2}` {
		t.Fatalf("executed = %v; resume must skip the recorded call", *world.executed)
	}
}

func TestTwoApprovalsAtOnceApplyOnce(t *testing.T) {
	world := newWorld(t)
	plans := agent.NewPlans(agent.NewMemoryPlanStore(), agent.PlanOptions{})
	pending := hold(t, world, plans, "ada")

	var wait sync.WaitGroup
	var mutex sync.Mutex
	won := 0
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := plans.Approve(context.Background(), world.mode, "ada", pending.ID); err == nil {
				mutex.Lock()
				won++
				mutex.Unlock()
			}
		}()
	}
	wait.Wait()
	if won != 1 {
		t.Fatalf("%d approvals succeeded", won)
	}
}

func TestEveryRunErrorNamesTheRunAndTheStage(t *testing.T) {
	world := newWorld(t)
	var events []agent.Event
	model := &script{`throw new Error("typo")`, `throw new Error("typo")`}
	result, err := agent.Run(context.Background(), world.mode, model, "list tasks", agent.Options{
		Runner: world.runner, Attempts: 2, OnEvent: func(event agent.Event) { events = append(events, event) },
	})
	var failure *agent.Error
	if !errors.As(err, &failure) || failure.RunID != result.RunID || failure.Stage != agent.StageRun {
		t.Fatalf("err = %#v", err)
	}
	if !strings.HasPrefix(err.Error(), "run "+result.RunID+": run: gave up after 2 attempts") {
		t.Fatalf("message = %q", err.Error())
	}
	var kinds []string
	for _, event := range events {
		if event.RunID != result.RunID {
			t.Fatalf("event %+v has another run ID", event)
		}
		kinds = append(kinds, event.Kind)
	}
	want := "run.started,search.done,program.written,program.failed,program.written,program.failed,run.failed"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("events = %v", kinds)
	}

	_, err = agent.Run(context.Background(), world.mode, &script{}, "list tasks", agent.Options{Runner: world.runner})
	if !errors.As(err, &failure) || failure.Stage != agent.StageGenerate {
		t.Fatalf("a model failure: %v", err)
	}
}
