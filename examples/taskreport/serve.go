package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	_ "github.com/mukezhz/codemode/sandbox/goja"
	_ "github.com/mukezhz/codemode/sandbox/subprocess"

	"github.com/mukezhz/codemode/examples/taskreport/server"
	"github.com/mukezhz/codemode/examples/taskreport/store"
	"github.com/mukezhz/codemode/examples/taskreport/web"
)

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

func serve(
	ctx context.Context,
	config config,
	tasks *store.Store,
	full capability.Registry,
	model llm.Client,
) error {
	assets, err := web.Assets()
	if err != nil {
		fmt.Printf("frontend\n  not built — %v\n", err)
	}

	// goja in-process by default; SANDBOX_ISOLATION=process for untrusted users.
	chatRunner, err := sandbox.NewFromEnv(sandbox.Options{MaxCalls: 25, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	defer chatRunner.Close()

	var audit io.Writer
	if config.audit != "" {
		file, err := os.OpenFile(config.audit, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		audit = file
	}

	handler, err := server.New(server.Options{
		Store:    tasks,
		Full:     full,
		Admins:   []string{"grace"},
		Model:    model,
		Runner:   chatRunner,
		Limit:    config.limit,
		Attempts: config.attempts,
		Assets:   assets,
		Logger:   logger(config),
		Programs: os.Stderr,
		Audit:    audit,
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

var _ = fs.FS(nil)
