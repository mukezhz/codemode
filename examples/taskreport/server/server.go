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
	"github.com/mukezhz/codemode/program"
	"github.com/mukezhz/codemode/sandbox"

	"github.com/mukezhz/codemode/examples/taskreport/app"
	"github.com/mukezhz/codemode/examples/taskreport/auth"
	"github.com/mukezhz/codemode/examples/taskreport/search"
	"github.com/mukezhz/codemode/examples/taskreport/store"
	"github.com/mukezhz/codemode/examples/taskreport/trace"
	"github.com/mukezhz/codemode/examples/taskreport/view"
)

type Options struct {
	Store    *store.Store
	Full     capability.Registry
	Admins   []string
	Model    llm.Client
	Runner   sandbox.Runner
	Limit    int
	Attempts int
	Assets   fs.FS
	Programs io.Writer
	Logger   *slog.Logger
	Cache    program.Cache
}

type Server struct {
	options Options
	app     *app.App
	log     *slog.Logger
}

func New(options Options) (*Server, error) {
	if options.Store == nil || options.Runner == nil {
		return nil, errors.New("server needs a store and a runner")
	}
	if options.Limit == 0 {
		options.Limit = 12
	}
	if options.Cache == nil {
		options.Cache = program.NewExactCache()
	}
	if options.Attempts == 0 {
		options.Attempts = 3
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	options.Runner = trace.Runner(options.Runner, logger, options.Programs)

	return &Server{
		options: options,
		app:     app.New(options.Store, options.Admins...),
		log:     logger,
	}, nil
}

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
	mux.HandleFunc("POST /api/chat/stream", server.chatStream)

	mux.Handle("/", server.assets())

	return server.withIdentity(mux)
}

// Demo reads headers; a real deployment must verify a session or token here.
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

func (server *Server) scoped(ctx context.Context) (codemode.CodeMode, capability.Registry, error) {
	identity, err := auth.From(ctx)
	if err != nil {
		return nil, capability.Registry{}, err
	}
	// Policy comes from (codemode.roles) in the protos, not a list here.
	allowed := server.options.Full.ForRole(string(identity.Role))
	mode, err := codemode.New(allowed, search.New(allowed), server.app.Execute)
	if err != nil {
		return nil, capability.Registry{}, err
	}
	return trace.CodeMode(mode, server.log.With("user", identity.User, "role", string(identity.Role))), allowed, nil
}

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

type chatRequest struct {
	Request string `json:"request"`
}

type chatResponse struct {
	Document view.Document      `json:"document"`
	Program  string             `json:"program"`
	Attempts int                `json:"attempts"`
	Used     []capability.Match `json:"used"`
	Took     string             `json:"took"`
	Cached   bool               `json:"cached"`
	Timing   agent.Timing       `json:"timing"`
}

// chatCallBudget bounds capability calls per answer, so workarounds like per-letter search loops fail.
const chatCallBudget = 25

func (server *Server) chat(writer http.ResponseWriter, request *http.Request) {
	body, identity, mode, ok := server.chatRequest(writer, request)
	if !ok {
		return
	}
	response, status, err := server.answer(request.Context(), body, identity, mode, nil)
	if err != nil {
		fail(writer, status, err)
		return
	}
	send(writer, response)
}

func (server *Server) chatStream(writer http.ResponseWriter, request *http.Request) {
	body, identity, mode, ok := server.chatRequest(writer, request)
	if !ok {
		return
	}

	flusher, streamable := writer.(http.Flusher)
	if !streamable {
		fail(writer, http.StatusInternalServerError, errors.New("this server cannot stream"))
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	// Stop reverse proxies from buffering the stream.
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)

	event := func(name string, value any) {
		payload, err := json.Marshal(value)
		if err != nil {
			return
		}
		fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", name, payload)
		flusher.Flush()
	}

	event("start", map[string]string{"request": body.Request})

	response, status, err := server.answer(request.Context(), body, identity, mode, func(piece string) {
		event("chunk", map[string]string{"text": piece})
	})
	if err != nil {
		event("error", map[string]any{"error": err.Error(), "status": status})
		return
	}
	event("result", response)
}

func (server *Server) chatRequest(writer http.ResponseWriter, request *http.Request) (chatRequest, auth.Identity, codemode.CodeMode, bool) {
	var body chatRequest
	if server.options.Model == nil {
		fail(writer, http.StatusServiceUnavailable,
			errors.New("no model is configured; restart with -api-key and -base-url"))
		return body, auth.Identity{}, nil, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<16)).Decode(&body); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return body, auth.Identity{}, nil, false
	}
	if strings.TrimSpace(body.Request) == "" {
		fail(writer, http.StatusBadRequest, errors.New("request is required"))
		return body, auth.Identity{}, nil, false
	}
	identity, err := auth.From(request.Context())
	if err != nil {
		fail(writer, http.StatusUnauthorized, err)
		return body, auth.Identity{}, nil, false
	}
	mode, _, err := server.scoped(request.Context())
	if err != nil {
		fail(writer, http.StatusUnauthorized, err)
		return body, auth.Identity{}, nil, false
	}
	return body, identity, mode, true
}

func (server *Server) answer(
	parent context.Context,
	body chatRequest,
	identity auth.Identity,
	mode codemode.CodeMode,
	onChunk func(string),
) (chatResponse, int, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()

	log := server.log.With("request", body.Request)
	started := time.Now()
	result, err := agent.Run(ctx, mode, trace.Model(server.options.Model, log), body.Request, agent.Options{
		Runner:       server.options.Runner,
		Limit:        server.options.Limit,
		Attempts:     server.options.Attempts,
		Instructions: view.Instructions + "\n\n" + identity.Instructions(),
		Cache:        server.options.Cache,
		// A program targets one role's capabilities, so cache per role.
		CacheCaller: string(identity.Role),
		OnChunk:     onChunk,
	})
	if err != nil {
		server.log.Error("chat failed", "request", body.Request, "error", err, "program", result.Source)
		return chatResponse{}, http.StatusBadGateway, err
	}

	document, err := view.Parse(result.Answer)
	if err != nil {
		server.log.Error("unrenderable result", "error", err, "program", result.Source)
		return chatResponse{}, http.StatusBadGateway, err
	}

	if document.Refused() {
		// A refusal is a successful response, not a server error.
		log.Info("step=answer", "refused", document.Unavailable, "attempts", result.Attempts)
	} else {
		log.Info("step=answer",
			"blocks", len(document.Blocks),
			"attempts", result.Attempts,
			"cached", result.Cached,
			"timing", result.Timing.String(),
			"took", time.Since(started).Round(time.Millisecond))
	}

	return chatResponse{

		Document: document,
		Program:  result.Source,
		Attempts: result.Attempts,
		Used:     result.Matches,
		Took:     time.Since(started).Round(time.Millisecond).String(),
		Cached:   result.Cached,
		Timing:   result.Timing,
	}, http.StatusOK, nil
}

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
		// SPA: serve the shell for unknown paths so reloading /chat works.
		if request.URL.Path != "/" && !strings.Contains(request.URL.Path, ".") {
			request = request.Clone(request.Context())
			request.URL.Path = "/"
		}
		files.ServeHTTP(writer, request)
	})
}

func send(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}

func fail(writer http.ResponseWriter, status int, err error) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(map[string]string{"error": err.Error()})
}
