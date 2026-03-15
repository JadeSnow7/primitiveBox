package pbui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embeddedDist embed.FS

// DistFS returns the built inspector UI assets.
func DistFS() (fs.FS, error) {
	return fs.Sub(embeddedDist, "dist")
}
