package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEnvFileParsesTheUsualShapes(t *testing.T) {
	path := write(t, `
# a comment
export EXPORTED=yes

QUOTED="  spaces kept  "
SINGLE='also kept'
BARE=plain
INLINE=value # this note is not part of the value
HASH="a#b"
EMPTY=
URL=https://api.deepseek.com/v1
`)
	for _, name := range []string{"EXPORTED", "QUOTED", "SINGLE", "BARE", "INLINE", "HASH", "EMPTY", "URL"} {
		os.Unsetenv(name)
		t.Cleanup(func() { os.Unsetenv(name) })
	}

	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"EXPORTED": "yes",
		"QUOTED":   "  spaces kept  ",
		"SINGLE":   "also kept",
		"BARE":     "plain",
		"INLINE":   "value",
		"HASH":     "a#b",
		"EMPTY":    "",
		"URL":      "https://api.deepseek.com/v1",
	} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestLoadEnvFileDoesNotOverrideTheEnvironment(t *testing.T) {
	t.Setenv("TASKREPORT_TEST_KEY", "from the shell")
	if err := loadEnvFile(write(t, "TASKREPORT_TEST_KEY=from the file\n")); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TASKREPORT_TEST_KEY"); got != "from the shell" {
		t.Fatalf("value = %q, want the shell's", got)
	}
}

func TestLoadEnvFileIgnoresAMissingFile(t *testing.T) {
	if err := loadEnvFile(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("a missing file was an error: %v", err)
	}
}

func TestLoadEnvFileRejectsAMalformedLine(t *testing.T) {
	err := loadEnvFile(write(t, "OPENAI_API_KEY sk-forgot-the-equals\n"))
	if err == nil {
		t.Fatal("loadEnvFile accepted a line with no =")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, ":1:") {
		t.Fatalf("error = %v, want it to name the line", err)
	}
}
