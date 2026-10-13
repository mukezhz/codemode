package capability

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
)

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

func LoadFile(path string) (Registry, error) {
	file, err := os.Open(path)
	if err != nil {
		return Registry{}, fmt.Errorf("open registry (generate one with codemode-registry): %w", err)
	}
	defer file.Close()
	return Load(file)
}

func LoadFS(filesystem fs.FS, path string) (Registry, error) {
	file, err := filesystem.Open(path)
	if err != nil {
		return Registry{}, fmt.Errorf("open registry: %w", err)
	}
	defer file.Close()
	return Load(file)
}
