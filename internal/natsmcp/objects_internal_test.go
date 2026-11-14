package natsmcp

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The folder of attachments of a machine is kept small: the oldest files go, the file just made stays, and nothing that is no
// regular file or is a temporary file of a fetch in progress is touched.
func TestTheFolderOfAttachmentsKeepsTheNewestFiles(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	var keep string
	for i := 0; i < 60; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.png", i))
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		keep = p
	}
	tmp := filepath.Join(dir, "fetch-1.tmp")
	if err := os.WriteFile(tmp, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	pruneAttachments(dir, keep)
	entries, _ := os.ReadDir(dir)
	var files int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" {
			files++
		}
	}
	if files != maxAttachDirLen {
		t.Errorf("%d files are left, want %d", files, maxAttachDirLen)
	}
	for _, gone := range []string{"f00.png", "f09.png"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s is the oldest and should be gone", gone)
		}
	}
	for _, stay := range []string{"f10.png", "f59.png", "fetch-1.tmp", "sub"} {
		if _, err := os.Stat(filepath.Join(dir, stay)); err != nil {
			t.Errorf("%s was removed: %v", stay, err)
		}
	}
	// the size counts too: the file that was just made stays even if it is the one that is over
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, make([]byte, (maxAttachDirMiB+1)<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	pruneAttachments(dir, big)
	if _, err := os.Stat(big); err != nil {
		t.Errorf("the new file was removed: %v", err)
	}
}
