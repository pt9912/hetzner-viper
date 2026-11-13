package model

import (
	"os"
	"path/filepath"
)

// StateDir is where viper keeps its state on this computer: ~/.local/state/viper. The directory that versions up to
// v0.21.1 used (~/.local/state/hetzner-claude) is moved there as a whole (best effort) when the new one does not exist yet.
func StateDir(home string) string {
	dir := filepath.Join(home, ".local", "state", "viper")
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	if old := filepath.Join(home, ".local", "state", "hetzner-claude"); isDir(old) {
		_ = os.Rename(old, dir)
	}
	return dir
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
