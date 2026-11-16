// Package plantest is the contract every agent.PlanStore passes: call Run from
// the adapter's own tests with a function that returns an empty store.
package plantest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/codemode"
)

func Run(t *testing.T, empty func(t *testing.T) agent.PlanStore) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	plan := func(id, owner string, status agent.PlanStatus, created time.Time) agent.Plan {
		return agent.Plan{
			ID: id, Owner: owner, RunID: "run-" + id, Request: "clean up", Status: status,
			Calls:     []codemode.Call{{Name: "tasks.delete", Input: []byte(`{"id":1}`)}},
			CreatedAt: created, ExpiresAt: created.Add(time.Hour),
			History: []agent.PlanChange{{Status: status, At: created, By: owner}},
		}
	}

	t.Run("round trip", func(t *testing.T) {
		store := empty(t)
		want := plan("p1", "ada", agent.PlanPending, at)
		if err := store.Create(ctx, want); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get(ctx, "ada", "p1")
		if err != nil {
			t.Fatal(err)
		}
		if got.RunID != "run-p1" || got.Request != "clean up" || len(got.Calls) != 1 || string(got.Calls[0].Input) != `{"id":1}` ||
			!got.ExpiresAt.Equal(want.ExpiresAt) || len(got.History) != 1 || got.Status != agent.PlanPending {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("owner scoped", func(t *testing.T) {
		store := empty(t)
		if err := store.Create(ctx, plan("p1", "ada", agent.PlanPending, at)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(ctx, "grace", "p1"); !errors.Is(err, agent.ErrNoPlan) {
			t.Fatalf("another owner read the plan: %v", err)
		}
		if _, err := store.Get(ctx, "ada", "nope"); !errors.Is(err, agent.ErrNoPlan) {
			t.Fatalf("missing plan: %v", err)
		}
		listed, err := store.List(ctx, "grace", "")
		if err != nil || len(listed) != 0 {
			t.Fatalf("another owner listed %d plans, err %v", len(listed), err)
		}
	})

	t.Run("list newest first, by status", func(t *testing.T) {
		store := empty(t)
		for _, item := range []agent.Plan{
			plan("old", "ada", agent.PlanPending, at),
			plan("new", "ada", agent.PlanPending, at.Add(time.Minute)),
			plan("done", "ada", agent.PlanApplied, at.Add(2*time.Minute)),
		} {
			if err := store.Create(ctx, item); err != nil {
				t.Fatal(err)
			}
		}
		pending, err := store.List(ctx, "ada", agent.PlanPending)
		if err != nil || len(pending) != 2 || pending[0].ID != "new" || pending[1].ID != "old" {
			t.Fatalf("pending = %+v, err %v", pending, err)
		}
		all, err := store.List(ctx, "ada", "")
		if err != nil || len(all) != 3 || all[0].ID != "done" {
			t.Fatalf("all = %+v, err %v", all, err)
		}
	})

	t.Run("update is compare and set", func(t *testing.T) {
		store := empty(t)
		if err := store.Create(ctx, plan("p1", "ada", agent.PlanPending, at)); err != nil {
			t.Fatal(err)
		}
		first, _ := store.Get(ctx, "ada", "p1")
		second, _ := store.Get(ctx, "ada", "p1")
		first.Status = agent.PlanApplying
		if err := store.Update(ctx, &first); err != nil {
			t.Fatal(err)
		}
		if first.Version != second.Version+1 {
			t.Fatalf("version = %d, want %d", first.Version, second.Version+1)
		}
		second.Status = agent.PlanRejected
		if err := store.Update(ctx, &second); !errors.Is(err, agent.ErrPlanChanged) {
			t.Fatalf("a stale update won: %v", err)
		}
		got, _ := store.Get(ctx, "ada", "p1")
		if got.Status != agent.PlanApplying {
			t.Fatalf("status = %s", got.Status)
		}
		stranger := got
		stranger.Owner = "grace"
		if err := store.Update(ctx, &stranger); err == nil {
			t.Fatal("another owner updated the plan")
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		store := empty(t)
		if err := store.Create(ctx, plan("p1", "ada", agent.PlanPending, at)); err != nil {
			t.Fatal(err)
		}
		if err := store.Create(ctx, plan("p1", "ada", agent.PlanPending, at)); err == nil {
			t.Fatal("created a plan twice")
		}
	})
}
