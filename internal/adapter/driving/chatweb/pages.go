package chatweb

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFiles embed.FS

func pages() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // the directory is embedded: it is there
	}
	return sub
}

// Pages is the embedded site (the tests look through it).
func Pages() fs.FS { return pages() }
