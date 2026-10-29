package pricecache_test

import (
	"os"
	"path/filepath"
	"testing"

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
