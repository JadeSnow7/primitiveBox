package pbui

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed fallback/*
var embeddedFallback embed.FS

// DistFS returns built inspector UI assets when they are available on disk.
// It falls back to a small embedded placeholder page so Go builds do not
// depend on generated frontend artifacts being checked into the repository.
func DistFS() (fs.FS, error) {
	for _, candidate := range []string{
		filepath.Join("cmd", "pb-ui", "dist"),
		"dist",
	} {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return os.DirFS(candidate), nil
		}
	}

	return fs.Sub(embeddedFallback, "fallback")
}
