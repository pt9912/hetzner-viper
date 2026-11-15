package settingsfile

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// Credentials (docs/plan/ui-bearbeiten.md, stage C): the editor sets or takes out the value of a credential in a file that
// exists. It changes the one statement (its span) and nothing else, keeps a copy of the file without credentials in a ZIP, and
// never says a value.

const (
	minSecret    = 8         // the redaction takes no shorter value out of an output
	maxSecret    = 8 << 10   // a token, a password
	maxFile      = 256 << 10 // a settings file
	keepBackups  = 5         // per file
	backupMaxAge = 30 * 24 * time.Hour
)

type credPlan struct {
	rel, path     string // the file, under the directory and complete
	before, after string
	etag          string
	lines         []string // what the change does, one line each (no values)
	names         []string // the credentials it touches
}

func etagOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func targetRel(ch settings.Change) (string, error) {
	switch ch.Target {
	case "base":
		return ".env.local", nil
	case "machine", "group":
		if !nameRe.MatchString(ch.Name) {
			return "", errors.New("name: lowercase letters, digits and hyphens")
		}
		if ch.Target == "machine" {
			return filepath.Join("machines", ch.Name+".env.local"), nil
		}
		return filepath.Join("groups", ch.Name+".env"), nil
	}
	return "", errors.New("unknown file")
}

// checkSecret is the value of a credential: text of a sensible size, one line, and long enough for the redaction to know it.
func checkSecret(v string) error {
	switch {
	case len(v) < minSecret:
		return fmt.Errorf("at least %d characters", minSecret)
	case len(v) > maxSecret:
		return fmt.Errorf("at most %d characters", maxSecret)
	case !utf8.ValidString(v):
		return errors.New("not text")
	case strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || (r >= 0x7f && r <= 0x9f) }):
		return errors.New("no line break or control character")
	}
	return nil
}

// plain refuses a file that is a link, in a link, or not a file.
func plainFile(dir, rel string) error {
	full := filepath.Join(dir, rel)
	if d := filepath.Dir(full); d != filepath.Clean(dir) {
		st, err := os.Lstat(d)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("there is no %s: make it first", filepath.ToSlash(rel))
		}
		if err != nil || !st.IsDir() || st.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link or no directory: change it at the terminal", filepath.ToSlash(filepath.Dir(rel)))
		}
	}
	st, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("there is no %s: make it first", filepath.ToSlash(rel))
	case err != nil:
		return err
	case st.Mode()&fs.ModeSymlink != 0 || !st.Mode().IsRegular():
		return fmt.Errorf("%s is a link or no file: change it at the terminal", filepath.ToSlash(rel))
	case st.Size() > maxFile:
		return fmt.Errorf("%s is larger than %d KiB: change it at the terminal", filepath.ToSlash(rel), maxFile>>10)
	}
	return nil
}

// only returns the one assignment of the name, or says why the file cannot be edited (several: which one would be meant).
func only(as []Assignment, name string) (a Assignment, found bool, err error) {
	n := 0
	for _, x := range as {
		if x.Name == name {
			a, n = x, n+1
		}
	}
	if n > 1 {
		return a, false, fmt.Errorf("%s is set %d times in the file: change it at the terminal", name, n)
	}
	return a, n == 1, nil
}

// setAssignment puts NAME='value' in place of the statement of the name, or at the end of the text.
func setAssignment(text, name, value string) (out string, existed bool, err error) {
	as, err := Assignments(text)
	if err != nil {
		return "", false, err
	}
	a, found, err := only(as, name)
	if err != nil {
		return "", false, err
	}
	stmt := name + "=" + quote(value)
	if found {
		return text[:a.Start] + stmt + text[a.End:], true, nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + stmt + "\n", false, nil
}

// removeAssignment takes the statement of the name out (its line, if nothing else is on it).
func removeAssignment(text, name string) (string, error) {
	as, err := Assignments(text)
	if err != nil {
		return "", err
	}
	a, found, err := only(as, name)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%s is not set in this file", name)
	}
	start, end := a.Start, a.End
	if (start == 0 || text[start-1] == '\n') && (end == len(text) || text[end] == '\n') && end < len(text) {
		end++
	}
	return text[:start] + text[end:], nil
}

// emptied is the text with the value of every credential taken out (NAME=”).
func emptied(text string) (string, error) {
	as, err := Assignments(text)
	if err != nil {
		return "", err
	}
	secrets := model.SecretNames()
	for i := len(as) - 1; i >= 0; i-- {
		if slices.Contains(secrets, as[i].Name) {
			text = text[:as[i].Start] + as[i].Name + "=''" + text[as[i].End:]
		}
	}
	return text, nil
}

// checkCredentials are the reasons a change of credentials is refused before any file is read.
func checkCredentials(ch settings.Change) (problems []string) {
	secrets := model.SecretNames()
	for n, v := range ch.Fields {
		if !slices.Contains(secrets, n) {
			problems = append(problems, n+": not a credential")
		} else if err := checkSecret(v); err != nil {
			problems = append(problems, n+": "+err.Error())
		}
		if slices.Contains(ch.Remove, n) {
			problems = append(problems, n+": set and removed at once")
		}
	}
	for _, n := range ch.Remove {
		if !slices.Contains(secrets, n) {
			problems = append(problems, n+": not a credential")
		}
	}
	if len(ch.Fields)+len(ch.Remove) == 0 {
		problems = append(problems, "nothing to change")
	}
	return problems
}

// edit makes the changes in the text of the plan, in the order of the names.
func (pl *credPlan) edit(ch settings.Change) error {
	set := make([]string, 0, len(ch.Fields))
	for n := range ch.Fields {
		set = append(set, n)
	}
	sort.Strings(set)
	for _, n := range set {
		next, existed, err := setAssignment(pl.after, n, ch.Fields[n])
		if err != nil {
			return err
		}
		pl.after = next
		pl.names = append(pl.names, n)
		verb := "+ " + n + ": set"
		if existed {
			verb = "~ " + n + ": replaced"
		}
		pl.lines = append(pl.lines, verb)
	}
	for _, n := range slices.Sorted(slices.Values(ch.Remove)) {
		next, err := removeAssignment(pl.after, n)
		if err != nil {
			return err
		}
		pl.after = next
		pl.names = append(pl.names, n)
		pl.lines = append(pl.lines, "- "+n+": removed")
	}
	return nil
}

func planCredential(dir string, ch settings.Change) (credPlan, []string) {
	var pl credPlan
	rel, err := targetRel(ch)
	if err != nil {
		return pl, []string{err.Error()}
	}
	pl.rel, pl.path = rel, filepath.Join(dir, rel)
	if err := plainFile(dir, rel); err != nil {
		return pl, []string{err.Error()}
	}
	b, err := os.ReadFile(pl.path)
	if err != nil {
		return pl, []string{err.Error()}
	}
	pl.before, pl.after, pl.etag = string(b), string(b), etagOf(b)
	if problems := checkCredentials(ch); len(problems) > 0 {
		return pl, problems
	}
	if err := pl.edit(ch); err != nil {
		return pl, []string{err.Error()}
	}
	if len(pl.after) > maxFile {
		return pl, []string{"the file would be larger than 256 KiB"}
	}
	return pl, nil
}

func previewCredential(dir string, ch settings.Change, env settings.Env) (settings.Preview, error) {
	var p settings.Preview
	pl, problems := planCredential(dir, ch)
	p.Problems = problems
	if len(problems) > 0 {
		return p, nil
	}
	p.Etag = pl.etag
	p.Files = []settings.File{{Path: filepath.ToSlash(pl.rel), Text: strings.Join(pl.lines, "\n") + "\n"}}
	problems2, broken := diffStatus(dir, env, pl.rel, pl.after, "")
	p.Problems = append(p.Problems, problems2...)
	sort.Strings(broken)
	p.Broken = broken
	for _, n := range ch.Remove {
		if v := env.Getenv(n); v != "" {
			p.Notes = append(p.Notes, n+": the environment of viper ui has it, and viper takes the value from there when the file does not set it")
		}
	}
	return p, nil
}

func createCredential(dir string, ch settings.Change, env settings.Env) (settings.Preview, error) {
	p, err := previewCredential(dir, ch, env)
	if err != nil {
		return p, err
	}
	if len(p.Problems) > 0 || len(p.Broken) > 0 {
		return p, errors.New("the change is refused")
	}
	if ch.Etag == "" || ch.Etag != p.Etag {
		p.Problems = append(p.Problems, "the file has changed since the preview: look at the preview again")
		return p, errors.New("the file has changed")
	}
	pl, _ := planCredential(dir, ch)
	name, err := writeBackup(env.Home, "credential", pl)
	if err != nil {
		p.Problems = append(p.Problems, "no backup could be made: "+err.Error())
		return p, errors.New("no backup")
	}
	p.Backup = name
	// the file must still be what the preview saw, right before it is replaced
	if cur, err := os.ReadFile(pl.path); err != nil || etagOf(cur) != pl.etag {
		p.Problems = append(p.Problems, "the file has changed since the preview: look at the preview again")
		return p, errors.New("the file has changed")
	}
	if err := replaceFile(pl.path, pl.after); err != nil {
		return p, err
	}
	return p, nil
}

// replaceFile puts new text in place of a file in one step: a file of its own next to it, the rights of the old one, then
// rename (Windows refuses it for a moment while another program has the old file open, so it tries for about a second).
func replaceFile(path, text string) error {
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".viper-edit-*")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(text)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), mode)
	}
	for i := 0; werr == nil && i < 50; i++ {
		if werr = os.Rename(tmp.Name(), path); werr == nil {
			return nil
		}
		if i < 49 {
			werr = nil
			time.Sleep(20 * time.Millisecond)
		}
	}
	_ = os.Remove(tmp.Name())
	return werr
}

// writeBackup keeps what the file was, without its credentials, in a ZIP of its own in the backups directory of the state
// (written once, never changed): the file under its logical name, and a manifest with the time, the action, the names of the
// credentials that change and the hashes of the saved text before and after. It returns the name of the ZIP.
func writeBackup(home, action string, pl credPlan) (string, error) {
	dir := filepath.Join(model.StateDir(home), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	before, err := emptied(pl.before)
	if err != nil {
		return "", err
	}
	after, err := emptied(pl.after)
	if err != nil {
		return "", err
	}
	now := time.Now()
	tmp, err := os.CreateTemp(dir, ".viper-backup-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename
	zw := zip.NewWriter(tmp)
	manifest := fmt.Sprintf("time: %s\naction: %s\nfiles:\n  - path: %s\n    saved-sha256: %s\n    after-sha256: %s\ncredentials: [%s]\nnote: the values of credentials are empty in the saved files\n",
		now.Format(time.RFC3339), action, filepath.ToSlash(pl.rel), etagOf([]byte(before)), etagOf([]byte(after)), strings.Join(pl.names, ", "))
	for _, e := range []struct{ name, text string }{{filepath.ToSlash(pl.rel), before}, {"manifest.yaml", manifest}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return "", err
		}
		if _, err := w.Write([]byte(e.text)); err != nil {
			return "", err
		}
	}
	err = zw.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o600)
	}
	if err != nil {
		return "", err
	}
	name := now.Format("20060102T150405") + "-" + action + ".zip"
	for i := 2; ; i++ { // two changes in a second
		if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s-%s-%d.zip", now.Format("20060102T150405"), action, i)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return "", err
	}
	pruneBackups(dir, now)
	return name, nil
}

// pruneBackups removes the backups older than 30 days, and those that are none of the five newest of any file they hold.
func pruneBackups(dir string, now time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".zip") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // the names begin with the time: the newest first
	seen := map[string]int{}
	for _, n := range names {
		files := filesOfBackup(filepath.Join(dir, n))
		keep := false
		for _, f := range files {
			seen[f]++
			keep = keep || seen[f] <= keepBackups
		}
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && now.Sub(st.ModTime()) > backupMaxAge {
			keep = false
		}
		if !keep {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
}

func filesOfBackup(path string) []string {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }() // read only
	var out []string
	for _, f := range r.File {
		if f.Name != "manifest.yaml" {
			out = append(out, f.Name)
		}
	}
	return out
}
