package pricecache_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/pricecache"
)

type value struct{ A int }

func TestWhatIsWrittenIsRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "viper")
	var v value
	if pricecache.Read(dir, "x.json", &v) {
		t.Error("nothing written yet")
	}
	pricecache.Write(dir, "x.json", value{7})
	if !pricecache.Read(dir, "x.json", &v) || v.A != 7 {
		t.Errorf("%+v", v)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestNoDirectoryIsNoCache(t *testing.T) {
	pricecache.Write("", "x.json", value{1})
	var v value
	if pricecache.Read("", "x.json", &v) {
		t.Error("no cache")
	}
}

func TestABrokenFileIsNotAHit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var v value
	if pricecache.Read(dir, "x.json", &v) {
		t.Error("broken file")
	}
}

type digest struct {
	Fetched time.Time `json:"fetched"`
	N       int       `json:"n"`
}

func TestGetKeepsAndReusesWhatItFetched(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	fetches := 0
	var failing bool
	get := func(usable func(*digest) bool) (*digest, error) {
		return pricecache.Get(context.Background(), dir, "d.json", clock,
			func(d *digest) time.Time { return d.Fetched }, usable,
			func(context.Context) (*digest, error) {
				fetches++
				if failing {
					return nil, errors.New("down")
				}
				return &digest{Fetched: now, N: fetches}, nil
			})
	}
	always := func(*digest) bool { return true }
	if d, err := get(always); err != nil || d.N != 1 || fetches != 1 {
		t.Fatalf("first: %+v %v %d", d, err, fetches)
	}
	now = now.Add(23 * time.Hour)
	if d, err := get(always); err != nil || d.N != 1 || fetches != 1 {
		t.Errorf("within a day: %+v %v %d", d, err, fetches)
	}
	// a digest that lacks what the current version needs is no digest
	if d, err := get(func(*digest) bool { return false }); err != nil || d.N != 2 || fetches != 2 {
		t.Errorf("not usable: %+v %v %d", d, err, fetches)
	}
	now = now.Add(25 * time.Hour)
	if d, err := get(always); err != nil || d.N != 3 || fetches != 3 {
		t.Errorf("after a day: %+v %v %d", d, err, fetches)
	}
	// the source is down: an old digest is better than none, up to a week
	failing = true
	now = now.Add(5 * 24 * time.Hour)
	if d, err := get(always); err != nil || d.N != 3 {
		t.Errorf("five days: %+v %v", d, err)
	}
	now = now.Add(3 * 24 * time.Hour)
	if _, err := get(always); err == nil {
		t.Error("eight days: want an error")
	}
}
