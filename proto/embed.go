// Package codemodeproto embeds the protobuf options that mark a service as a
// code mode capability surface.
package codemodeproto

import _ "embed"

// CodeMode is the contents of codemode.proto.
//
//go:embed codemode.proto
var CodeMode []byte

// Path is where the file is conventionally written in a consuming repository.
const Path = "codemode.proto"
