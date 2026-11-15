package settings

import "io/fs"

// FS is how the settings files are read. nil (Env.Files) is the disk; a preview reads the files as they would be after a
// change, without writing anything.
type FS interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]fs.DirEntry, error)
}

// Change is a request to make new settings files (docs/plan/ui-bearbeiten.md, stage A).
type Change struct {
	// Kind is "machine" (machines/<Name>.env.local), "group" (groups/<Name>.env) or "base" (.env.local; Name is empty).
	Kind string
	Name string
	// Fields are the settings to write (names from a list the editor knows, never a credential).
	Fields map[string]string
	// Members are the machines of a group.
	Members []string
}

// File is a file a change would make: its path under the settings directory and its text.
type File struct {
	Path string
	Text string
}

// Preview is what a change would do, written nowhere.
type Preview struct {
	Files []File
	// Problems are the reasons the change is refused: nothing is written as long as there is one.
	Problems []string
	// Broken are the machines and groups that load now and would fail with the change.
	Broken []string
	// Notes say what viper up would still ask for (credentials).
	Notes []string
}

// Editor makes new settings files. It writes only files that are not there yet.
type Editor interface {
	// Preview checks the change like viper up would read the files after it, for all machines and groups.
	Preview(dir string, ch Change, env Env) (Preview, error)
	// Create writes the files of a change that Preview accepts. A file that exists is an error (nothing is replaced).
	Create(dir string, ch Change, env Env) (Preview, error)
}
