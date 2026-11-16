package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func source(names ...string) (Source, *[]string) {
	var called []string
	registry := capability.Registry{}
	for _, name := range names {
		registry.Capabilities = append(registry.Capabilities, capability.Capability{
			Name: name, RPC: "rpc/" + name,
			Input: capability.TypeSchema{Fields: map[string]string{"id": "integer"}, Required: []string{"id"}},
		})
	}
	return Source{Registry: registry, Execute: func(_ context.Context, rpc string, _ json.RawMessage) (json.RawMessage, error) {
		called = append(called, rpc)
		return json.RawMessage(`{}`), nil
	}}, &called
}

func TestCombineRoutesEachRPCToItsSource(t *testing.T) {
	tasks, tasksCalled := source("tasks.close")
	users, usersCalled := source("users.ban")
	combined, err := Combine(tasks, users)
	if err != nil {
		t.Fatal(err)
	}
	for _, rpc := range []string{"rpc/tasks.close", "rpc/users.ban"} {
		if _, err := combined.Execute(context.Background(), rpc, nil); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(*tasksCalled, ",") != "rpc/tasks.close" || strings.Join(*usersCalled, ",") != "rpc/users.ban" {
		t.Fatalf("tasks = %v, users = %v", *tasksCalled, *usersCalled)
	}
	if _, err := combined.Execute(context.Background(), "rpc/other", nil); err == nil {
		t.Fatal("an RPC no source serves ran")
	}
	if _, err := Combine(tasks, tasks); err == nil {
		t.Fatal("combined two sources serving the same RPC")
	}
}

func TestInvalidInputNeverReachesTheHost(t *testing.T) {
	tasks, called := source("tasks.close")
	mode, err := FromSource(tasks, capability.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &Recorder{}
	_, err = mode.Execute(WithRecorder(context.Background(), recorder), "tasks.close", json.RawMessage(`{"id":"7"}`))
	if err == nil || !strings.Contains(err.Error(), `id: want integer, got "7"`) {
		t.Fatalf("err = %v", err)
	}
	if len(*called) != 0 || len(recorder.Calls()) != 0 || recorder.Wrote() {
		t.Fatalf("called = %v, calls = %v", *called, recorder.Calls())
	}
}

func approvalSource() (Source, *[]string) {
	var called []string
	return Source{
		Registry: capability.Registry{Capabilities: []capability.Capability{
			{Name: "tasks.list", RPC: "list", ReadOnly: true},
			{Name: "tasks.rename", RPC: "rename"},
			{Name: "tasks.delete", RPC: "delete", NeedsApproval: true},
		}},
		Execute: func(_ context.Context, rpc string, _ json.RawMessage) (json.RawMessage, error) {
			called = append(called, rpc)
			return json.RawMessage(`{"ok":true}`), nil
		},
	}, &called
}

func TestAWriteThatNeedsApprovalHoldsItAndEveryLaterWrite(t *testing.T) {
	tasks, called := approvalSource()
	mode, err := FromSource(tasks, capability.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &Recorder{}
	ctx := WithRecorder(context.Background(), recorder)
	for _, name := range []string{"tasks.rename", "tasks.delete", "tasks.list", "tasks.rename"} {
		if _, err := mode.Execute(ctx, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(*called, ",") != "rename,list" {
		t.Fatalf("ran %v; want the first rename and the read only", *called)
	}
	if !recorder.Held() {
		t.Fatal("recorder does not report a pending write")
	}
	var held []string
	for _, call := range recorder.Calls() {
		if call.Held {
			held = append(held, call.Name)
		}
	}
	if strings.Join(held, ",") != "tasks.delete,tasks.rename" {
		t.Fatalf("held = %v", held)
	}

	*called = nil
	approved := WithApproval(context.Background())
	if _, err := mode.Execute(approved, "tasks.delete", nil); err != nil || strings.Join(*called, ",") != "delete" {
		t.Fatalf("approved delete: err = %v, ran %v", err, *called)
	}
}

func TestAWriteThatNeedsApprovalNeverRunsWithoutARecorder(t *testing.T) {
	tasks, called := approvalSource()
	mode, err := FromSource(tasks, capability.Caller{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mode.Execute(context.Background(), "tasks.delete", nil)
	if !errors.Is(err, ErrPermanent) || len(*called) != 0 {
		t.Fatalf("err = %v, ran %v", err, *called)
	}
}

func TestFromSourceKeepsOnlyWhatTheCallerMayCall(t *testing.T) {
	open, _ := source("tasks.list")
	admin, _ := source("users.ban")
	admin.Registry = admin.Registry.Require("admin", func(capability.Capability) bool { return true })
	combined, err := Combine(open, admin)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := FromSource(combined, capability.Caller{Roles: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mode.Execute(context.Background(), "users.ban", json.RawMessage(`{"id":1}`)); err == nil {
		t.Fatal("a member called an admin capability")
	}
	matches, err := mode.Search(context.Background(), "users ban tasks list", 5)
	if err != nil || len(matches) != 1 || matches[0].Capability.Name != "tasks.list" {
		t.Fatalf("matches = %v, err = %v", matches, err)
	}
}
