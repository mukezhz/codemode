package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables read by OptionsFromEnv.
const (
	// EnvEngine selects the interpreter: goja, starlark, or lua.
	EnvEngine = "SANDBOX_ENGINE"
	// EnvTimeout bounds a run, as a Go duration.
	EnvTimeout = "SANDBOX_TIMEOUT"
	// EnvMaxCalls limits capability calls per run.
	EnvMaxCalls = "SANDBOX_MAX_CALLS"
)

// OptionsFromEnv fills every zero field of options from the environment.
func OptionsFromEnv(options Options) (Options, error) {
	if options.Engine == "" {
		options.Engine = strings.ToLower(strings.TrimSpace(os.Getenv(EnvEngine)))
	}
	if options.Timeout == 0 {
		if value := strings.TrimSpace(os.Getenv(EnvTimeout)); value != "" {
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return options, fmt.Errorf("%s: %w", EnvTimeout, err)
			}
			options.Timeout = timeout
		}
	}
	if options.MaxCalls == 0 {
		if value := strings.TrimSpace(os.Getenv(EnvMaxCalls)); value != "" {
			maxCalls, err := strconv.Atoi(value)
			if err != nil {
				return options, fmt.Errorf("%s: %w", EnvMaxCalls, err)
			}
			options.MaxCalls = maxCalls
		}
	}
	return options, nil
}

// NewFromEnv is OptionsFromEnv followed by New.
func NewFromEnv(options Options) (Runner, error) {
	options, err := OptionsFromEnv(options)
	if err != nil {
		return nil, err
	}
	return New(options)
}
