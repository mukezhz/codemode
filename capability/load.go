package capability

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// Load decodes a registry produced by codemode-registry.
//
// The registry is usually embedded, so the binary carries its own allowlist and
// cannot drift from the file on disk:
//
//	//go:embed registry.json
//	var registryJSON []byte
//
//	registry, err := capability.Load(bytes.NewReader(registryJSON))
func Load(reader io.Reader) (Registry, error) {
	var registry Registry
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, fmt.Errorf("decode registry: %w", err)
	}
	if len(registry.Capabilities) == 0 {
		return Registry{}, fmt.Errorf("registry has no capabilities")
	}
	return registry, nil
}

// LoadFile reads and decodes the registry at path.
func LoadFile(path string) (Registry, error) {
	file, err := os.Open(path)
	if err != nil {
		return Registry{}, fmt.Errorf("open registry (generate one with codemode-registry): %w", err)
	}
	defer file.Close()
	return Load(file)
}

// LoadFS reads and decodes the registry at path within filesystem, which is
// what an embed.FS gives you.
func LoadFS(filesystem fs.FS, path string) (Registry, error) {
	file, err := filesystem.Open(path)
	if err != nil {
		return Registry{}, fmt.Errorf("open registry: %w", err)
	}
	defer file.Close()
	return Load(file)
}
