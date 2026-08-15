// Package web embeds the built frontend.
//
// The dist directory is produced by `pnpm build` and committed, so `go run`
// serves a working application without Node installed. During development run
// the Vite dev server instead: it proxies /api to this process and gives you
// hot reload.
package web

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// ErrNotBuilt reports that the frontend has not been built.
var ErrNotBuilt = errors.New("run `pnpm install && pnpm build` in examples/taskreport/web")

// Assets returns the built frontend rooted at dist.
func Assets() (fs.FS, error) {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, err
	}
	// An empty dist embeds as a directory with nothing in it, which would
	// serve blank pages rather than say what went wrong.
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return nil, ErrNotBuilt
	}
	return dist, nil
}
