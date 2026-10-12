package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadEnvFile reads KEY=VALUE lines into the environment; a missing file is fine and existing variables win.
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

// unquote strips one matching pair of quotes.
func unquote(value string) string {
	if len(value) >= 2 {
		for _, quote := range []byte{'"', '\''} {
			if value[0] == quote && value[len(value)-1] == quote {
				return value[1 : len(value)-1]
			}
		}
	}
	// Unquoted values end at an inline comment.
	if index := strings.Index(value, " #"); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}
