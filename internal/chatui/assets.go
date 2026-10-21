package chatui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var files embed.FS

func Assets() fs.FS {
	assets, _ := fs.Sub(files, "dist")
	return assets
}
