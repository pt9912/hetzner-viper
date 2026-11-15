package settingsfile

import (
	"io/fs"
	"os"

	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// readFile and readDir read the settings files through env.Files (the disk if it is nil), so that a preview can read the files
// as they would be after a change.
func readFile(env settings.Env, path string) ([]byte, error) {
	if env.Files != nil {
		return env.Files.ReadFile(path)
	}
	return os.ReadFile(path)
}

func readDir(env settings.Env, path string) ([]fs.DirEntry, error) {
	if env.Files != nil {
		return env.Files.ReadDir(path)
	}
	return os.ReadDir(path)
}
