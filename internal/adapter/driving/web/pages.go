package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFiles embed.FS

// pages are the files of the browser: HTML, CSS and JavaScript, no other.
func pages() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // the directory is in the program
	}
	return sub
}

// Pages is the embedded site (the tests look through it).
func Pages() fs.FS { return pages() }
