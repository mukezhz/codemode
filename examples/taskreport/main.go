package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/llm/openai"
	coderegistry "github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"

	"github.com/mukezhz/codemode/capability"
	taskv1 "github.com/mukezhz/codemode/examples/taskreport/gen/task/v1"
	userv1 "github.com/mukezhz/codemode/examples/taskreport/gen/user/v1"

	"github.com/mukezhz/codemode/examples/taskreport/app"
	"github.com/mukezhz/codemode/examples/taskreport/auth"
	"github.com/mukezhz/codemode/examples/taskreport/search"
	"github.com/mukezhz/codemode/examples/taskreport/store"
	"github.com/mukezhz/codemode/examples/taskreport/trace"
)

func main() {
	// Loaded before flag parsing so the file can supply flag defaults.
	if err := loadEnvFile(envFile()); err != nil {
		fmt.Fprintln(os.Stderr, "taskreport:", err)
		os.Exit(1)
	}

	var config config
	flag.StringVar(&config.request, "request", "give me a status report across every project", "what to ask")
	flag.StringVar(&config.database, "db", "tasks.db", "SQLite file; :memory: keeps nothing")
	flag.StringVar(&config.baseURL, "base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible endpoint, e.g. https://api.deepseek.com/v1")
	flag.StringVar(&config.apiKey, "api-key", os.Getenv("OPENAI_API_KEY"), "API key for that endpoint")
	flag.StringVar(&config.model, "model", envOr("OPENAI_MODEL", "deepseek-chat"), "model name")
	flag.Float64Var(&config.temperature, "temperature", -1, "sampling temperature; negative leaves the provider default")
	flag.IntVar(&config.limit, "limit", 12, "how many capabilities to describe to the model")
	flag.IntVar(&config.attempts, "attempts", 3, "how many programs may be generated before giving up")
	flag.StringVar(&config.instructions, "instructions", "", "extra guidance appended to the system prompt")
	flag.StringVar(&config.user, "user", "ada", "who is asking; tasks are scoped to them")
	flag.StringVar(&config.role, "role", "member", "member or admin; decides which capabilities exist")
	flag.BoolVar(&config.seed, "seed", true, "seed a sample backlog when the database is empty")
	flag.BoolVar(&config.verbose, "v", false, "print the system prompt as well as the program")
	flag.StringVar(&config.serve, "serve", "", "serve the web app on this address, e.g. :8080")
	flag.StringVar(&config.restrict, "restrict", "",
		"comma-separated capability names to make admin-only at runtime, for testing denial without editing the protos")
	flag.StringVar(&config.logLevel, "log", "info", "debug, info, warn, or error; debug shows the prompt, the program, and every capability call")
	flag.StringVar(&config.audit, "audit", "", "append one JSON line per capability call to this file when serving")
	flag.Parse()

	if err := run(context.Background(), config); err != nil {
		fmt.Fprintln(os.Stderr, "taskreport:", err)
		os.Exit(1)
	}
}

type config struct {
	request      string
	database     string
	baseURL      string
	apiKey       string
	model        string
	temperature  float64
	limit        int
	attempts     int
	instructions string
	user         string
	role         string
	seed         bool
	verbose      bool
	serve        string
	logLevel     string
	audit        string
	restrict     string
}

func run(ctx context.Context, config config) error {
	tasks, err := store.Open(config.database)
	if err != nil {
		return err
	}
	defer tasks.Close()

	if config.seed {
		seeded, err := seed(tasks)
		if err != nil {
			return err
		}
		if seeded > 0 {
			fmt.Printf("seeded %d tasks into %s\n\n", seeded, config.database)
		}
	}

	// Identity goes on the context only; no capability takes a user, so a program cannot name one.
	role, err := auth.ParseRole(config.role)
	if err != nil {
		return err
	}
	identity := auth.Identity{User: config.user, Role: role}
	ctx = auth.NewContext(ctx, identity)

	full, err := capabilities()
	if err != nil {
		return err
	}
	full, err = restrict(full, config.restrict)
	if err != nil {
		return err
	}

	// A member's runtime lacks admin capabilities entirely, not just hidden from the model.
	allowed := forRole(full, identity)

	// Search shapes what the model sees; only the registry is a security boundary.
	mode, err := codemode.New(allowed, search.New(allowed), app.New(tasks, adminsOf(config)...).Execute)
	if err != nil {
		return err
	}

	// goja in-process by default; SANDBOX_ISOLATION=process runs it in codemode-worker.
	runner, err := sandbox.NewFromEnv(sandbox.Options{MaxCalls: 60, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	defer runner.Close()

	runner = trace.Runner(runner, logger(config), os.Stderr)

	withheld := len(full.Capabilities) - len(allowed.Capabilities)
	fmt.Printf("request\n  %s\n\nsigned in as\n  %s (%s)\n\ncapabilities\n  %d across %s",
		config.request, identity.User, identity.Role,
		len(allowed.Capabilities), strings.Join(allowed.Namespaces(), ", "))
	if withheld > 0 {
		fmt.Printf("  — %d withheld from this role", withheld)
	}
	fmt.Printf("\n\nsandbox\n  %s\n\n", runner.Name())

	if config.apiKey == "" && config.baseURL == "" {
		if config.serve != "" {
			return serve(ctx, config, tasks, full, nil)
		}
		return offline(ctx, mode, runner, config)
	}
	model, err := openai.New(openai.Options{Options: llm.Options{
		APIKey:      config.apiKey,
		BaseURL:     config.baseURL,
		Model:       config.model,
		Temperature: temperature(config.temperature),
		Timeout:     2 * time.Minute,
	}})
	if err != nil {
		return err
	}
	fmt.Printf("model\n  %s at %s\n\n", model.Name(), endpointLabel(config.baseURL))

	if config.serve != "" {
		return serve(ctx, config, tasks, full, model)
	}

	result, err := agent.Run(ctx, mode, model, config.request, agent.Options{
		Runner:       runner,
		Limit:        config.limit,
		Attempts:     config.attempts,
		Instructions: strings.TrimSpace(identity.Instructions() + "\n" + config.instructions),
	})
	report(result, config.verbose, runner)
	if err != nil {
		return err
	}
	fmt.Printf("answer\n%s\n", indent(agent.Answer(result.Answer)))
	return nil
}

func envFile() string {
	if path := strings.TrimSpace(os.Getenv("TASKREPORT_ENV")); path != "" {
		return path
	}
	return "examples/taskreport/.env"
}

func capabilities() (capability.Registry, error) {
	tasks, err := coderegistry.FromFiles(taskv1.File_task_proto)
	if err != nil {
		return capability.Registry{}, err
	}
	users, err := coderegistry.FromFiles(userv1.File_user_proto)
	if err != nil {
		return capability.Registry{}, err
	}
	return capability.Merge(tasks, users)
}

func restrict(full capability.Registry, names string) (capability.Registry, error) {
	names = strings.TrimSpace(names)
	if names == "" {
		return full, nil
	}
	wanted := strings.Split(names, ",")
	for index, name := range wanted {
		wanted[index] = strings.TrimSpace(name)
	}

	// Unknown names would silently restrict nothing.
	known := map[string]bool{}
	for _, item := range full.Capabilities {
		known[item.Name] = true
	}
	for _, name := range wanted {
		if !known[name] {
			return capability.Registry{}, fmt.Errorf("-restrict names %q, which is not a capability; try one of %s",
				name, strings.Join(full.Names(), ", "))
		}
	}

	restricted := full.Require(string(auth.RoleAdmin), capability.Named(wanted...))
	fmt.Printf("restricted\n  %s now require the admin role\n\n", strings.Join(wanted, ", "))
	return restricted, nil
}

func forRole(full capability.Registry, identity auth.Identity) capability.Registry {
	return full.ForRole(string(identity.Role))
}

func adminsOf(config config) []string {
	if strings.EqualFold(config.role, string(auth.RoleAdmin)) {
		return []string{config.user}
	}
	return []string{"grace"}
}

// temperature converts the flag's sentinel into the library's tri-state, since 0 is a real setting.
func temperature(value float64) *float64 {
	if value < 0 {
		return nil
	}
	return llm.Float(value)
}

func report(result agent.Result, verbose bool, runner sandbox.Runner) {
	if len(result.Matches) > 0 {
		fmt.Println("search selected")
		for _, match := range result.Matches {
			fmt.Printf("  %-26s %s\n", match.Capability.Name, match.Capability.Description)
		}
		fmt.Println()
		if verbose {
			fmt.Printf("system prompt\n%s\n\n", indent(runner.Prompt(result.Matches)))
		}
	}
	fmt.Printf("timing\n  %s\n\n", result.Timing)
}

func offline(ctx context.Context, mode codemode.CodeMode, runner sandbox.Runner, config config) error {
	fmt.Println("model\n  none — pass -api-key and -base-url to have one write the program")

	matches, err := mode.Search(ctx, config.request, config.limit)
	if err != nil {
		return err
	}
	fmt.Println("\nsearch selected")
	for _, match := range matches {
		fmt.Printf("  %-26s %s\n", match.Capability.Name, match.Capability.Description)
	}
	if config.verbose {
		fmt.Printf("\nsystem prompt\n%s\n", indent(runner.Prompt(matches)))
	}

	value, err := runner.Run(ctx, mode, fixedProgram)
	if err != nil {
		return err
	}
	fmt.Printf("\nanswer\n%s\n", indent(agent.Answer(value)))
	return nil
}

const fixedProgram = `
const counts = codemode.execute("reports.statusCounts", {});
const projects = codemode.execute("reports.byProject", {}).groups;
const priorities = codemode.execute("reports.byPriority", {}).groups;
const overdue = codemode.execute("reports.overdue", {}).tasks;
const throughput = codemode.execute("reports.throughput", { days: 7 }).days;

const lines = [];
lines.push("Backlog: " + counts.open + " open, " + counts.completed + " done, " +
           counts.overdue + " overdue, " + counts.due_today + " due today.");

const behind = projects
	.filter(project => project.open > 0)
	.sort((a, b) => b.open - a.open);
if (behind.length > 0) {
	lines.push("Open work by project: " +
		behind.map(project => project.name + " (" + project.open + ")").join(", ") + ".");
}

const high = priorities.find(priority => priority.name === "high");
if (high && high.open > 0) {
	lines.push(high.open + " high-priority tasks are still open.");
}

if (overdue.length > 0) {
	const worst = overdue.slice(0, 3)
		.map(task => task.title + " (due " + task.due_date + ")")
		.join("; ");
	lines.push("Oldest overdue: " + worst + ".");
} else {
	lines.push("Nothing is overdue.");
}

const week = throughput.reduce((total, day) => total + day.completed, 0);
lines.push("Completed in the last 7 days: " + week + ".");

return lines.join("\n");
`

func seed(tasks *store.Store) (int, error) {
	existing, err := tasks.List(store.Everyone(), store.Filter{Limit: 1})
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		return 0, nil
	}

	day := func(offset int) string {
		return time.Now().AddDate(0, 0, offset).Format("2006-01-02")
	}

	samples := []struct {
		owner     string
		title     string
		project   string
		priority  string
		due       string
		tags      []string
		completed bool
	}{
		{"ada", "Ship the reporting endpoint", "platform", "high", day(-6), []string{"backend"}, false},
		{"ada", "Fix flaky auth test", "platform", "high", day(-2), []string{"backend", "ci"}, false},
		{"ada", "Upgrade the SQLite driver", "platform", "low", day(14), []string{"chore"}, false},
		{"ada", "Migrate the build to Buf", "platform", "medium", day(-9), []string{"chore"}, true},

		{"linus", "Write the onboarding guide", "docs", "medium", day(-1), []string{"writing"}, false},
		{"linus", "Record a demo video", "docs", "low", "", []string{"writing"}, false},
		{"linus", "Publish the API reference", "docs", "medium", day(-4), []string{"writing"}, true},

		{"grace", "Redesign the empty state", "design", "medium", day(3), []string{"ui"}, false},
		{"grace", "Audit colour contrast", "design", "high", day(0), []string{"ui", "a11y"}, false},
		{"grace", "Draft the Q3 roadmap", "planning", "medium", day(7), nil, false},
		{"grace", "Pick an icon set", "design", "low", day(-3), []string{"ui"}, true},
		{"grace", "Close out Q2 planning", "planning", "high", day(-8), nil, true},
	}

	for _, sample := range samples {
		created, err := tasks.Create(sample.owner, sample.title, sample.project,
			sample.priority, sample.due, sample.tags)
		if err != nil {
			return 0, err
		}
		if sample.completed {
			if _, err := tasks.Complete(store.Only(sample.owner), created.ID); err != nil {
				return 0, err
			}
		}
	}
	return len(samples), nil
}

func endpointLabel(baseURL string) string {
	if baseURL == "" {
		return "api.openai.com"
	}
	return baseURL
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func indent(text string) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ")
}
