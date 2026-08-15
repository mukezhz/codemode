// Package server puts the task application behind HTTP.
//
// Two surfaces, and the split is the point:
//
//   - /api/tasks and friends are an ordinary REST API. The board in the browser
//     uses these. No model is involved, because creating a task does not need
//     one.
//   - /api/chat takes a sentence, and one model turn produces one program that
//     composes capabilities and returns a view document to render.
//
// A real product looks like this. Code mode is not a replacement for the app's
// own API; it is the thing that answers the questions the API has no endpoint
// for.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mukezhz/codemode/agent"
	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/sandbox"

	"github.com/mukezhz/codemode/examples/taskreport/app"
	"github.com/mukezhz/codemode/examples/taskreport/auth"
	"github.com/mukezhz/codemode/examples/taskreport/search"
	"github.com/mukezhz/codemode/examples/taskreport/store"
	"github.com/mukezhz/codemode/examples/taskreport/trace"
	"github.com/mukezhz/codemode/examples/taskreport/view"
)

// Options configures the server. Everything is supplied by the caller; nothing
// here reads the environment.
type Options struct {
	Store *store.Store
	// Full is the whole capability surface. It is narrowed per request from
	// the caller's role.
	Full capability.Registry
	// Admins names the workspace administrators.
	Admins []string
	// Model answers chat requests. Nil disables /api/chat with a clear error
	// rather than a panic, so the board still works without credentials.
	Model llm.Client
	// Runner evaluates the generated programs. Required.
	Runner sandbox.Runner
	// Limit is how many capabilities are described to the model.
	Limit int
	// Attempts is how many programs may be generated before giving up.
	Attempts int
	// Assets serves the built frontend. Nil serves a message explaining how to
	// build it.
	Assets fs.FS
	// Programs receives each generated program, formatted for reading. Nil
	// prints none.
	Programs io.Writer
	// Logger records every step of a request. Nil discards them.
	Logger *slog.Logger
}

// Server is the HTTP handler.
type Server struct {
	options Options
	app     *app.App
	log     *slog.Logger
}

// New builds a Server.
func New(options Options) (*Server, error) {
	if options.Store == nil || options.Runner == nil {
		return nil, errors.New("server needs a store and a runner")
	}
	if options.Limit == 0 {
		options.Limit = 12
	}
	if options.Attempts == 0 {
		options.Attempts = 3
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	// Wrapped once here rather than per request: a Runner is safe to share, and
	// each Run creates a fresh interpreter.
	options.Runner = trace.Runner(options.Runner, logger, options.Programs)

	return &Server{
		options: options,
		app:     app.New(options.Store, options.Admins...),
		log:     logger,
	}, nil
}

// Handler returns the mux, with identity resolved for every /api request.
func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/session", server.session)
	mux.HandleFunc("GET /api/tasks", server.listTasks)
	mux.HandleFunc("POST /api/tasks", server.createTask)
	mux.HandleFunc("POST /api/tasks/{id}/complete", server.completeTask)
	mux.HandleFunc("POST /api/tasks/{id}/reopen", server.reopenTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", server.deleteTask)
	mux.HandleFunc("GET /api/summary", server.summary)
	mux.HandleFunc("POST /api/chat", server.chat)

	// The frontend is a single-page app, so any unmatched path serves the
	// shell and lets the router decide.
	mux.Handle("/", server.assets())

	return server.withIdentity(mux)
}

// withIdentity resolves who is asking, once, for the whole request.
//
// A real deployment verifies a session cookie or a bearer token here. The demo
// reads two headers so a browser can switch users from a dropdown — which is
// exactly the shortcut a real one must not take.
func (server *Server) withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		user := strings.TrimSpace(request.Header.Get("X-Demo-User"))
		if user == "" {
			user = "ada"
		}
		role, err := auth.ParseRole(request.Header.Get("X-Demo-Role"))
		if err != nil {
			fail(writer, http.StatusBadRequest, err)
			return
		}
		identity := auth.Identity{User: user, Role: role}
		next.ServeHTTP(writer, request.WithContext(auth.NewContext(request.Context(), identity)))
	})
}

// scoped builds the runtime this request may use: the registry narrowed to the
// caller's role, and search built over that same narrowed set so the model is
// never told about something it cannot call.
func (server *Server) scoped(ctx context.Context) (codemode.CodeMode, capability.Registry, error) {
	identity, err := auth.From(ctx)
	if err != nil {
		return nil, capability.Registry{}, err
	}
	// The policy comes from (codemode.roles) in the contracts, so this server
	// holds no list of privileged capability names.
	allowed := server.options.Full.ForRole(string(identity.Role))
	mode, err := codemode.New(allowed, search.New(allowed), server.app.Execute)
	if err != nil {
		return nil, capability.Registry{}, err
	}
	// Wrapping here rather than inside the library is the point: the seam is an
	// interface, so observing it needs no change to the thing observed.
	return trace.CodeMode(mode, server.log.With("user", identity.User, "role", string(identity.Role))), allowed, nil
}

// ---------------------------------------------------------------- the API

func (server *Server) session(writer http.ResponseWriter, request *http.Request) {
	identity, err := auth.From(request.Context())
	if err != nil {
		fail(writer, http.StatusUnauthorized, err)
		return
	}
	_, allowed, err := server.scoped(request.Context())
	if err != nil {
		fail(writer, http.StatusInternalServerError, err)
		return
	}
	send(writer, map[string]any{
		"user":         identity.User,
		"role":         string(identity.Role),
		"capabilities": allowed.Names(),
		"namespaces":   allowed.Namespaces(),
		"withheld":     len(server.options.Full.Capabilities) - len(allowed.Capabilities),
		"model":        server.modelName(),
	})
}

func (server *Server) modelName() string {
	if server.options.Model == nil {
		return ""
	}
	return server.options.Model.Name()
}

// The board's endpoints go through the same App as the capabilities do, so
// there is one implementation of what "complete a task" means and one place
// that scopes it to the caller.
func (server *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	input := map[string]any{}
	for _, field := range []string{"project", "status", "priority", "tag"} {
		if value := request.URL.Query().Get(field); value != "" {
			input[field] = value
		}
	}
	server.execute(writer, request, "tasks.list", input)
}

func (server *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<16)).Decode(&body); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	server.execute(writer, request, "tasks.create", body)
}

func (server *Server) completeTask(writer http.ResponseWriter, request *http.Request) {
	server.byID(writer, request, "tasks.complete")
}

func (server *Server) reopenTask(writer http.ResponseWriter, request *http.Request) {
	server.byID(writer, request, "tasks.reopen")
}

func (server *Server) deleteTask(writer http.ResponseWriter, request *http.Request) {
	server.byID(writer, request, "tasks.delete")
}

func (server *Server) byID(writer http.ResponseWriter, request *http.Request, name string) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil {
		fail(writer, http.StatusBadRequest, fmt.Errorf("id must be a number"))
		return
	}
	server.execute(writer, request, name, map[string]any{"id": id})
}

func (server *Server) summary(writer http.ResponseWriter, request *http.Request) {
	server.execute(writer, request, "reports.statusCounts", map[string]any{})
}

// execute runs one capability for the board. It goes through CodeMode rather
// than calling the store directly, so the registry authorizes these calls
// exactly as it authorizes a program's.
func (server *Server) execute(writer http.ResponseWriter, request *http.Request, name string, input map[string]any) {
	mode, _, err := server.scoped(request.Context())
	if err != nil {
		fail(writer, http.StatusUnauthorized, err)
		return
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	output, err := mode.Execute(request.Context(), name, encoded)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Write(output)
}

// ---------------------------------------------------------------- the chat

type chatRequest struct {
	Request string `json:"request"`
}

type chatResponse struct {
	Document view.Document      `json:"document"`
	Program  string             `json:"program"`
	Attempts int                `json:"attempts"`
	Used     []capability.Match `json:"used"`
	Took     string             `json:"took"`
}

// chatCallBudget bounds one answer.
//
// It is deliberately close to what a report needs. A model that cannot answer
// directly will otherwise find a way around: asked for every task with only a
// search capability, one wrote a loop calling search once per letter of the
// alphabet and reassembled the list from the results. Thirty-six calls, a
// correct-looking answer, and a pattern nobody wants in production.
//
// A budget this size makes that fail, and the failure reaches the model as
// something it can act on: the repair attempt is told the budget was exhausted.
const chatCallBudget = 25

func (server *Server) chat(writer http.ResponseWriter, request *http.Request) {
	if server.options.Model == nil {
		fail(writer, http.StatusServiceUnavailable,
			errors.New("no model is configured; restart with -api-key and -base-url"))
		return
	}

	var body chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<16)).Decode(&body); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Request) == "" {
		fail(writer, http.StatusBadRequest, errors.New("request is required"))
		return
	}

	identity, err := auth.From(request.Context())
	if err != nil {
		fail(writer, http.StatusUnauthorized, err)
		return
	}
	mode, _, err := server.scoped(request.Context())
	if err != nil {
		fail(writer, http.StatusUnauthorized, err)
		return
	}

	// The model call is the slow part, so the deadline lives here rather than
	// on the sandbox, which bounds only the program.
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Minute)
	defer cancel()

	log := server.log.With("request", body.Request)
	started := time.Now()
	result, err := agent.Run(ctx, mode, trace.Model(server.options.Model, log), body.Request, agent.Options{
		Runner:   server.options.Runner,
		Limit:    server.options.Limit,
		Attempts: server.options.Attempts,
		// Two things the model cannot work out for itself: the shape of the
		// answer, and the shape of its own access.
		Instructions: view.Instructions + "\n\n" + identity.Instructions(),
	})
	if err != nil {
		server.log.Error("chat failed", "request", body.Request, "error", err, "program", result.Source)
		fail(writer, http.StatusBadGateway, err)
		return
	}

	document, err := view.Parse(result.Answer)
	if err != nil {
		// The program ran but produced something unrenderable. Returning the
		// source with the error is what makes this debuggable rather than
		// mysterious.
		server.log.Error("unrenderable result", "error", err, "program", result.Source)
		fail(writer, http.StatusBadGateway, err)
		return
	}

	if document.Refused() {
		// A refusal is a successful request with an unhappy answer, not a
		// server failure, so it comes back as one — the browser renders it
		// differently, and the program is still attached for inspection.
		log.Info("step=answer", "refused", document.Unavailable, "attempts", result.Attempts)
	} else {
		log.Info("step=answer",
			"blocks", len(document.Blocks),
			"attempts", result.Attempts,
			"took", time.Since(started).Round(time.Millisecond))
	}

	send(writer, chatResponse{
		Document: document,
		Program:  result.Source,
		Attempts: result.Attempts,
		Used:     result.Matches,
		Took:     time.Since(started).Round(time.Millisecond).String(),
	})
}

// ---------------------------------------------------------------- assets

func (server *Server) assets() http.Handler {
	if server.options.Assets == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(writer, "The frontend is not built.\n\n"+
				"  cd examples/taskreport/web && pnpm install && pnpm build\n\n"+
				"Or run the Vite dev server on :5173, which proxies /api here.\n")
		})
	}

	files := http.FileServerFS(server.options.Assets)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// A single-page app owns its own routing, so a path with no file
		// behind it serves the shell rather than a 404 — otherwise reloading
		// /chat breaks.
		if request.URL.Path != "/" && !strings.Contains(request.URL.Path, ".") {
			request = request.Clone(request.Context())
			request.URL.Path = "/"
		}
		files.ServeHTTP(writer, request)
	})
}

// ---------------------------------------------------------------- helpers

func send(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		// The status is already written by now, so logging is all that is left.
		return
	}
}

func fail(writer http.ResponseWriter, status int, err error) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(map[string]string{"error": err.Error()})
}
