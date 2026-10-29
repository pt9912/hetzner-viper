// Package pricecache keeps what the catalogs digest of a price list in small files in the cache
// directory, so that a large list is read once a day and not at every run. How old a digest may be
// is up to the catalog; the file only holds it.
package pricecache

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Read fills v from the file name in dir; ok is false if there is none or it cannot be read. An
// empty dir is no cache.
func Read(dir, name string, v any) (ok bool) {
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	return err == nil && json.Unmarshal(b, v) == nil
}

// Write keeps v in the file name in dir. A cache that cannot be written only costs the next run a
// new download, so there is no error.
func Write(dir, name string, v any) {
	b, err := json.Marshal(v)
	if dir == "" || err != nil || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	// a file of its own, then rename: two runs at once do not read half a file
	tmp, err := os.CreateTemp(dir, "price-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, name)) != nil {
		_ = os.Remove(tmp.Name())
	}
}
