package search_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	taskv1 "github.com/mukezhz/codemode/examples/taskreport/gen/task/v1"
	userv1 "github.com/mukezhz/codemode/examples/taskreport/gen/user/v1"
	"github.com/mukezhz/codemode/examples/taskreport/search"
	coderegistry "github.com/mukezhz/codemode/registry"
)

func full(t *testing.T) capability.Registry {
	t.Helper()
	tasks, err := coderegistry.FromFiles(taskv1.File_task_proto)
	if err != nil {
		t.Fatal(err)
	}
	users, err := coderegistry.FromFiles(userv1.File_user_proto)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}
	return merged
}

func names(matches []capability.Match) []string {
	found := make([]string, len(matches))
	for index, match := range matches {
		found[index] = match.Capability.Name
	}
	return found
}

func TestSearchAlwaysFillsTheLimit(t *testing.T) {
	registry := full(t)
	find := search.New(registry)

	for _, query := range []string{
		"how many todos do I have",
		"how many todo list does i have created",
		"xyzzy plugh nothing matches this at all",
		"",
	} {
		matches, err := find(context.Background(), query, 12)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 12 {
			t.Errorf("%q returned %d capabilities, want the full 12: %v",
				query, len(matches), names(matches))
		}
	}

	matches, _ := find(context.Background(), "anything", 999)
	if len(matches) != len(registry.Capabilities) {
		t.Errorf("returned %d, want all %d", len(matches), len(registry.Capabilities))
	}
}

func TestCountingQuestionsReachACountingCapability(t *testing.T) {
	find := search.New(full(t))

	for _, query := range []string{
		"how many todos do I have",
		"how many tasks have I created",
		"how many todo list does i have created",
		"what is on my plate",
		"how much work is left",
	} {
		matches, err := find(context.Background(), query, 12)
		if err != nil {
			t.Fatal(err)
		}
		offered := names(matches)
		if !slices.Contains(offered, "tasks.list") && !slices.Contains(offered, "reports.statusCounts") {
			t.Errorf("%q offered neither tasks.list nor reports.statusCounts: %v", query, offered)
		}
	}
}

func TestSynonymsBridgeTheVocabularyGap(t *testing.T) {
	find := search.New(full(t))

	for query, want := range map[string]string{
		"list my todos":         "tasks.list",
		"what is late":          "reports.overdue",
		"show me finished work": "reports.completedBetween",
		"who is on the team":    "users.list",
	} {
		matches, err := find(context.Background(), query, 5)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(names(matches), want) {
			t.Errorf("%q did not surface %s in its top 5: %v", query, want, names(matches))
		}
	}
}

func TestQuantityWordsDoNotRank(t *testing.T) {
	find := search.New(full(t))
	matches, err := find(context.Background(), "how many todos do I have", 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(names(matches)[0], "projects.") {
		t.Fatalf("a project capability ranked first for a question about todos: %v", names(matches))
	}
}
