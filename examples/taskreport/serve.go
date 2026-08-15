package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/llm"
	"github.com/mukezhz/codemode/sandbox"

	"github.com/mukezhz/codemode/examples/taskreport/server"
	"github.com/mukezhz/codemode/examples/taskreport/store"
	"github.com/mukezhz/codemode/examples/taskreport/web"
)

// logger builds the request logger. -log debug adds the whole prompt, the
// generated program, and one line per capability call, which is what you want
// open the first time an answer looks wrong.
func logger(config config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(config.logLevel)) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// serve runs the web application until interrupted.
func serve(
	ctx context.Context,
	config config,
	tasks *store.Store,
	full capability.Registry,
	runner sandbox.Runner,
	model llm.Client,
) error {
	assets, err := web.Assets()
	if err != nil {
		fmt.Printf("frontend\n  not built — %v\n", err)
	}

	handler, err := server.New(server.Options{
		Store:    tasks,
		Full:     full,
		Admins:   []string{"grace"},
		Model:    model,
		Runner:   runner,
		Limit:    config.limit,
		Attempts: config.attempts,
		Assets:   assets,
		Logger:   logger(config),
		Programs: os.Stderr,
	})
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              config.serve,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("serving\n  http://localhost%s\n", config.serve)
	if model == nil {
		fmt.Println("  the board works; /api/chat needs -api-key and -base-url")
	}
	fmt.Println()

	// Shut down on interrupt rather than dropping in-flight requests, which is
	// what makes the reload-the-page loop pleasant during development.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	errs := make(chan error, 1)
	go func() { errs <- httpServer.ListenAndServe() }()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	}
}

// assetsOrNil keeps the signature above honest: a missing build is a warning,
// not a failure, because the API is still useful on its own.
var _ = fs.FS(nil)
