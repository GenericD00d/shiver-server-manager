package web

import (
	"embed"
	"io/fs"
)

// The built front end (internal/web/ui, built with "npm run build").
//
//go:embed all:dist
var dist embed.FS

// StaticFS is the front end's files.
func StaticFS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
