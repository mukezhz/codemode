package codemodeproto

import _ "embed"

//go:embed codemode.proto
var CodeMode []byte

const Path = "codemode.proto"
