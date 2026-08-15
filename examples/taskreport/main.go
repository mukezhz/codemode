// Command taskreport is a task tracker with reporting, driven in code mode.
//
// It is a complete application: tasks live in SQLite, eighteen capabilities
// are generated from protobuf contracts, and a model answers questions by
// writing one JavaScript program that composes them. The program runs in goja
// with a single host binding, and only its final answer comes back.
//
// The model is reached over the OpenAI protocol, so any endpoint speaking it
// works — DeepSeek, a local server, or OpenAI itself:
//
//	go run ./examples/taskreport \
//	  -base-url https://api.deepseek.com/v1 \
//	  -api-key  sk-... \
//	  -model    deepseek-chat \
//	  -request  "which projects are behind, and what should I do first?"
//
// With no key it still runs end to end: search still selects capabilities, and
// a fixed program stands in for the model so the mechanism is inspectable.
//
// Note how configuration flows. This command reads flags and the environment;
// the library reads neither. Everything below is handed an explicit Options.
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
	"github.com/mukezhz/codemode/sandbox/goja"

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
	// Before the flags are parsed, so the file can supply their defaults. The
	// path is fixed rather than a flag for that reason; -env would have to be
	// read before flag.Parse to be useful.
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
	flag.StringVar(&config.logLevel, "log", "info", "debug, info, warn, or error; debug shows the prompt, the program, and every capability call")
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
}

func run(ctx context.Context, config config) error {
	// 1. The application's own database. The library never sees it.
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

	// 2. Who is asking. In a service this comes from a verified token; here
	// from flags. It goes on the context and nowhere else — no capability
	// takes a user, so a program cannot name one.
	role, err := auth.ParseRole(config.role)
	if err != nil {
		return err
	}
	identity := auth.Identity{User: config.user, Role: role}
	ctx = auth.NewContext(ctx, identity)

	// 3. The registry: what may run. One call per proto file, joined here.
	// This is how a larger surface is assembled — a CRM would list contacts,
	// deals, and billing the same way, each owning its own namespace.
	full, err := capabilities()
	if err != nil {
		return err
	}

	// 4. Narrowed to this caller's role. A member's runtime does not contain
	// tasks.assign at all: it is absent from the map Execute resolves against,
	// not merely hidden from the model.
	allowed := forRole(full, identity)

	// 5. The runtime. Search shapes what the model knows about; the registry
	// shapes what may execute. Two separate lists, and only the second is a
	// security boundary.
	mode, err := codemode.New(allowed, search.New(allowed), app.New(tasks, adminsOf(config)...).Execute)
	if err != nil {
		return err
	}

	// 6. The sandbox. goja, bounded: this program may make at most 60
	// capability calls and run for at most 30 seconds.
	runner, err := goja.New(sandbox.Options{MaxCalls: 60, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	defer runner.Close()

	// Wrapped so the program that reaches the interpreter is printed. This is
	// the code the model actually produced, after the agent stripped whatever
	// markdown fences it wrapped it in.
	runner = trace.Runner(runner, logger(config), os.Stderr)

	withheld := len(full.Capabilities) - len(allowed.Capabilities)
	fmt.Printf("request\n  %s\n\nsigned in as\n  %s (%s)\n\ncapabilities\n  %d across %s",
		config.request, identity.User, identity.Role,
		len(allowed.Capabilities), strings.Join(allowed.Namespaces(), ", "))
	if withheld > 0 {
		fmt.Printf("  — %d withheld from this role", withheld)
	}
	fmt.Printf("\n\nsandbox\n  %s\n\n", runner.Name())

	// 7. The model, configured entirely from this command's own flags.
	if config.apiKey == "" && config.baseURL == "" {
		if config.serve != "" {
			// The board works without a model; only the chat needs one.
			return serve(ctx, config, tasks, full, runner, nil)
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
		return serve(ctx, config, tasks, full, runner, model)
	}

	result, err := agent.Run(ctx, mode, model, config.request, agent.Options{
		Runner:       runner,
		Limit:        config.limit,
		Attempts:     config.attempts,
		Instructions: config.instructions,
	})
	report(result, config.verbose, runner)
	if err != nil {
		return err
	}
	fmt.Printf("answer\n%s\n", indent(agent.Answer(result.Answer)))
	return nil
}

// envFile is the dotfile this command reads. TASKREPORT_ENV moves it, for
// running several configurations without editing one file.
func envFile() string {
	if path := strings.TrimSpace(os.Getenv("TASKREPORT_ENV")); path != "" {
		return path
	}
	return "examples/taskreport/.env"
}

// capabilities assembles the whole surface from every proto file the
// application publishes. Merge rejects a name claimed twice, so two subsystems
// colliding on a namespace is a startup failure rather than a routing surprise.
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

// adminOnly are the capabilities that reach across owners. Naming them in one
// place beats scattering role checks through the proto or the dispatcher.
var adminOnly = capability.Named(
	"users.list",
	"tasks.listFor",
	"tasks.assign",
	"reports.byOwner",
)

// forRole returns the registry this caller may execute against.
//
// Building a registry per request is cheap — it is a slice and a map — so the
// narrowing happens here rather than as a check deep inside the application.
// The checks inside stay anyway; this is the outer of two locks.
func forRole(full capability.Registry, identity auth.Identity) capability.Registry {
	if identity.Admin() {
		return full
	}
	return full.Select(func(item capability.Capability) bool { return !adminOnly(item) })
}

func adminsOf(config config) []string {
	if strings.EqualFold(config.role, string(auth.RoleAdmin)) {
		return []string{config.user}
	}
	// The seeded workspace has one administrator, so users.list can show a
	// role that is not "member" for everyone.
	return []string{"grace"}
}

// temperature converts the flag's sentinel into the library's tri-state. A
// temperature of 0 is a real setting, so "unset" cannot be the zero value.
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
	// The program itself is printed by trace.Runner as it reaches the sandbox,
	// so repeating it here would only make the two drift.
}

// offline runs without a model so the whole path stays inspectable without
// credentials. Everything except the program's authorship is unchanged: search
// still ranks, the registry still authorizes, the sandbox still calls the host.
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

// fixedProgram is what a model would plausibly write for the default request.
// It is the argument for code mode in one screen: eight capability calls, all
// the filtering and totalling done in the sandbox, and one string returned. As
// ordinary tool use that is eight round trips with every intermediate result
// re-entering the model's context.
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

// seed writes a sample backlog on first run, dated relative to today so the
// overdue and throughput reports have something to say.
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

	// Three owners, so scoping is visible: run as ada and you see four tasks,
	// run as an admin and you see all twelve.
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
