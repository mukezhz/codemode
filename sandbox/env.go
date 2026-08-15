// This file is the only place in the package that reads the process
// environment. New takes an Options and consults nothing else.

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
	// EnvTimeout stops a program once it has run this long, as a Go duration.
	EnvTimeout = "SANDBOX_TIMEOUT"
	// EnvMaxCalls limits how many capability calls a program may make.
	EnvMaxCalls = "SANDBOX_MAX_CALLS"
)

// OptionsFromEnv fills every zero field of options from the environment,
// leaving set fields alone. Your configuration wins; the environment is the
// fallback.
//
//	options, err := sandbox.OptionsFromEnv(sandbox.Options{MaxCalls: 20})
//	runner, err := sandbox.New(options)   // MaxCalls stays 20
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
