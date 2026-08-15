// Package codemodeproto embeds the protobuf options that mark a service as a
// code mode capability surface.
//
// A consumer needs code_mode.proto in its own buf module to write
// (code_mode.namespace) and (code_mode.description). Rather than copying a
// file that must then stay in step with the library, write it out from here:
//
//	codemode-registry -print-proto > proto/code_mode.proto
//
// The file is versioned with the module, so `go get -u` and a re-run keep the
// options and the generator on the same definition.
package codemodeproto

import _ "embed"

// CodeMode is the contents of code_mode.proto.
//
//go:embed code_mode.proto
var CodeMode []byte

// Path is where the file is conventionally written in a consuming repository.
// It is what the import line in your own protos must resolve to.
const Path = "code_mode.proto"
