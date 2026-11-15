package settings

import (
	"io"
	"io/fs"
)

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
	// Kind "credential" changes a file that exists: Target is "base", "machine" or "group" and Name the machine or group;
	// Fields maps a credential (a name of model.SecretNames) to its new value, Remove lists the credentials to take out, and
	// Etag is the state of the file the preview saw (a file that changed since is refused).
	Target string
	Remove []string
	Etag   string
	// Confirm is the word that a change of a dangerous setting asks to be typed (the preview names it).
	Confirm string
}

// SettingInfo is a setting that Kind "setting" may change: its kind of value ("text", "bool", "enum", "int") and the limits.
type SettingInfo struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
	Min     int      `json:"min"`
	Max     int      `json:"max"`
	Danger  bool     `json:"danger"`
	// Help is what the setting does, by language ("en", "de").
	Help map[string]string `json:"help"`
}

// BackupInfo is a backup of a change: the file name, the time it was made, the files it holds.
type BackupInfo struct {
	Name  string   `json:"name"`
	Time  string   `json:"time"`
	Files []string `json:"files"`
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
	// Etag is the state of the file a credential change would replace (apply sends it back).
	Etag string
	// Backup is the file the change left in the backups directory (after Create).
	Backup string
	// Confirm says why a change asks to be confirmed, and ConfirmWord is what has to be typed.
	Confirm     []string
	ConfirmWord string
}

// Editor makes new settings files, and sets the credentials of the files that exist (a copy without the credentials is kept
// of what it changes).
type Editor interface {
	// Preview checks the change like viper up would read the files after it, for all machines and groups.
	Preview(dir string, ch Change, env Env) (Preview, error)
	// Create writes the files of a change that Preview accepts. A file that exists is an error (nothing is replaced).
	Create(dir string, ch Change, env Env) (Preview, error)
	// ReadSetting gives the value of a setting that is text over several lines (target "base", "machine" or "group", and the name).
	ReadSetting(dir, target, name, setting string, env Env) (string, error)
	// Catalog lists the settings that Kind "setting" may change.
	Catalog() []SettingInfo
	// Backups lists the backups of changes (the newest first); OpenBackup opens one of them by its name (a name of the list).
	Backups(env Env) ([]BackupInfo, error)
	OpenBackup(env Env, name string) (io.ReadCloser, error)
}
