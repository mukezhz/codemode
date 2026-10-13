package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvEngine   = "SANDBOX_ENGINE"
	EnvTimeout  = "SANDBOX_TIMEOUT"
	EnvMaxCalls = "SANDBOX_MAX_CALLS"
	EnvWorker   = "SANDBOX_WORKER"
	EnvMaxHeap  = "SANDBOX_MAX_HEAP_BYTES"
)

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
	if len(options.Worker) == 0 {
		options.Worker = strings.Fields(os.Getenv(EnvWorker))
	}
	if options.MaxHeapBytes == 0 {
		if value := strings.TrimSpace(os.Getenv(EnvMaxHeap)); value != "" {
			maxHeap, err := strconv.Atoi(value)
			if err != nil {
				return options, fmt.Errorf("%s: %w", EnvMaxHeap, err)
			}
			options.MaxHeapBytes = maxHeap
		}
	}
	return options, nil
}

func NewFromEnv(options Options) (Runner, error) {
	options, err := OptionsFromEnv(options)
	if err != nil {
		return nil, err
	}
	return New(options)
}
