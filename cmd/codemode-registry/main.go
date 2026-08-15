// Command codemode-registry turns a Buf descriptor set into registry.json,
// the allowlist of capabilities a model may call.
//
// Install it, or run it without installing:
//
//	go run github.com/mukezhz/codemode/cmd/codemode-registry@latest -help
//
// A typical build step, from anywhere in your repository:
//
//	buf build --as-file-descriptor-set -o build/protoset.pb
//	codemode-registry -descriptor build/protoset.pb -out internal/capabilities/registry.json
//
// To start a project, write out the options file your protos must import:
//
//	codemode-registry -print-proto > proto/codemode.proto
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mukezhz/codemode/registry"

	codemodeproto "github.com/mukezhz/codemode/proto"
)

func main() {
	descriptor := flag.String("descriptor", "registry/protoset.pb",
		"path to a Buf file descriptor set (buf build --as-file-descriptor-set)")
	out := flag.String("out", "registry/registry.json",
		"path to write the generated registry; - writes to stdout")
	printProto := flag.Bool("print-proto", false,
		"write codemode.proto to stdout and exit, for importing into your own protos")
	quiet := flag.Bool("quiet", false, "suppress the summary line")
	flag.Parse()

	if err := run(*descriptor, *out, *printProto, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "codemode-registry:", err)
		os.Exit(1)
	}
}

func run(descriptorPath, outPath string, printProto, quiet bool) error {
	if printProto {
		_, err := os.Stdout.Write(codemodeproto.CodeMode)
		return err
	}

	descriptor, err := os.ReadFile(descriptorPath)
	if err != nil {
		return fmt.Errorf("read descriptor set (build one with `buf build --as-file-descriptor-set -o %s`): %w",
			descriptorPath, err)
	}

	generated, err := registry.Parse(descriptor)
	if err != nil {
		return err
	}

	output, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		return err
	}
	output = append(output, '\n')

	if outPath == "-" {
		_, err := os.Stdout.Write(output)
		return err
	}
	if directory := filepath.Dir(outPath); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(outPath, output, 0o644); err != nil {
		return err
	}
	if !quiet {
		fmt.Printf("generated %s with %d capabilities\n", outPath, len(generated.Capabilities))
	}
	return nil
}
