package main

import (
	"context"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/codemode/sourcetest"
	"github.com/mukezhz/codemode/sandbox"
)

func TestOneProgramCallsAllThreeSources(t *testing.T) {
	ctx := context.Background()
	all, closeSources, err := sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSources()
	sourcetest.Run(t, all)

	mode, err := codemode.FromSource(all, capability.Caller{Roles: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := sandbox.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	value, err := runner.Run(ctx, mode, program)
	if err != nil {
		t.Fatal(err)
	}
	if value != "Scheduled today: Write the docs at 14:00" {
		t.Fatalf("value = %v", value)
	}
}

func TestImportedToolsNeedTheRoleTheHostNamed(t *testing.T) {
	all, closeSources, err := sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeSources()
	for _, name := range all.Registry.For(capability.Caller{}).Names() {
		if name == "calendar.events" {
			t.Fatal("a caller with no role can reach an imported tool")
		}
	}
}
