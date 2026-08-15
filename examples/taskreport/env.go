package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadEnvFile reads KEY=VALUE lines into the process environment.
//
// This lives in the application, not the library. The library reads no
// environment at all — every constructor takes explicit options — and a
// dotfile is one more thing an application decides to consult, not something a
// package should do behind its caller's back.
//
// A missing file is not an error: the flags and the real environment still
// work, and a demo should not insist on a file you may not want.
//
// Values already present in the environment win, so `API_KEY=… go run …` beats
// whatever the file says. Flags beat both, because they default from the
// environment and an explicit flag overrides that default.
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")

		key, value, found := strings.Cut(text, "=")
		if !found {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, line)
		}

		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(value))); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// unquote strips one matching pair of quotes. Quoting is what lets a value keep
// leading spaces or a # that would otherwise read as a comment.
func unquote(value string) string {
	if len(value) >= 2 {
		for _, quote := range []byte{'"', '\''} {
			if value[0] == quote && value[len(value)-1] == quote {
				return value[1 : len(value)-1]
			}
		}
	}
	// Unquoted values end at an inline comment, so `KEY=value # note` is
	// `value` rather than `value # note`.
	if index := strings.Index(value, " #"); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}
