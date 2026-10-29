// Package pricecache keeps what the catalogs digest of a price list in small files in the cache
// directory, so that a large list is read once a day and not at every run. How old a digest may be
// is up to the catalog; the file only holds it.
package pricecache

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	// MaxAge is how long a digest is used before the price lists are read again.
	MaxAge = 24 * time.Hour
	// MaxStale is how old a digest may be when the price lists cannot be read: an old digest is better
	// than none, but prices of a month ago are not a basis for a comparison.
	MaxStale = 7 * 24 * time.Hour
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

// Get is the digest kept in the file name in dir if it is usable and no older than MaxAge, else the
// one fetch makes (and keeps). If fetch fails, a kept digest of up to MaxStale is used. stamp says when
// a digest was made and usable whether one is complete (a digest from an older version of viper may
// lack what the current one needs).
func Get[T any](ctx context.Context, dir, name string, now func() time.Time, stamp func(*T) time.Time, usable func(*T) bool, fetch func(context.Context) (*T, error)) (*T, error) {
	var cached T
	have := Read(dir, name, &cached) && usable(&cached)
	if have && now().Sub(stamp(&cached)) < MaxAge {
		return &cached, nil
	}
	fresh, err := fetch(ctx)
	switch {
	case err == nil:
		Write(dir, name, fresh)
		return fresh, nil
	case have && ctx.Err() == nil && now().Sub(stamp(&cached)) < MaxStale:
		return &cached, nil
	}
	return nil, err
}
