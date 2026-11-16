package agent

import (
	"context"
	"strings"
	"testing"
)

type summary struct {
	Open  int      `json:"open" desc:"todos not done"`
	Names []string `json:"names"`
	Note  string   `json:"note,omitempty"`
}

func TestRunAsRepairsAnAnswerOfTheWrongShape(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{
		`return "two open";`,
		`return { open: 1.5, names: [] };`,
		`const { todos } = codemode.execute("todos.list", {}); return { open: todos.length, names: ["a"] };`,
	}}

	answer, result, err := RunAs[summary](context.Background(), mode, author, "summarize", Options{Runner: newRunner(t)})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Open != 1 || strings.Join(answer.Names, ",") != "a" || result.Attempts != 3 {
		t.Fatalf("answer = %+v, attempts = %d", answer, result.Attempts)
	}
	if !strings.Contains(author.systems[0], "summary { names: repeated string, note?: string, open: integer (todos not done) }") {
		t.Fatalf("system prompt does not describe the shape:\n%s", author.systems[0])
	}
	if !strings.Contains(author.prompts[1], "want object summary") || !strings.Contains(author.prompts[2], "open: want integer, got 1.5") {
		t.Fatalf("repair prompts = %q", author.prompts[1:])
	}
}

func TestRunAsGivesUpWithTheLastShapeError(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`return {};`, `return {};`}}
	_, _, err := RunAs[summary](context.Background(), mode, author, "summarize", Options{Runner: newRunner(t), Attempts: 2})
	if err == nil || !strings.Contains(err.Error(), "names: required") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAsString(t *testing.T) {
	mode, _ := effects(t)
	author := &model{responses: []string{`return 3;`, `return "three";`}}
	answer, _, err := RunAs[string](context.Background(), mode, author, "count", Options{Runner: newRunner(t)})
	if err != nil || answer != "three" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
}
