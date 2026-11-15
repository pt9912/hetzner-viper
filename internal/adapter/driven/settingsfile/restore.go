package settingsfile

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// Restore (docs/plan/ui-bearbeiten.md, stage B): put the files of a backup back in place of the files that are there now. The ZIP is
// ours (the state directory of the user), but it is read as if it were not: only the files the manifest names, by the names a
// settings file has, of a size that is limited, as plain text; paths and rights are never taken from it. The credentials are not
// in it, so the ones of the files that are there stay; a value of a credential in the ZIP is refused.

var restorable = regexp.MustCompile(`^(\.env\.local|machines/[a-z0-9]([a-z0-9-]*[a-z0-9])?\.env\.local|groups/[a-z0-9]([a-z0-9-]*[a-z0-9])?\.env)$`)

const maxBackupSize = 2 << 20 // a backup of at most five settings files

// readBackup reads the files of a backup by its name: the logical path of each and its text.
func readBackup(env settings.Env, name string) (map[string]string, error) {
	if !backupName.MatchString(name) {
		return nil, errors.New("no such backup")
	}
	path := filepath.Join(model.StateDir(env.Home), "backups", name)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("no such backup")
	}
	if st.Size() > maxBackupSize {
		return nil, errors.New("the backup is larger than it can be")
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, errors.New("the backup is no ZIP")
	}
	defer func() { _ = r.Close() }() // read only
	if len(r.File) > keepBackups+1 {
		return nil, errors.New("the backup holds more files than a change makes")
	}
	files := map[string]string{}
	for _, f := range r.File {
		if f.Name == "manifest.yaml" {
			continue
		}
		text, err := readEntry(f)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", f.Name, err)
		}
		if _, dup := files[f.Name]; dup {
			return nil, errors.New("a file is in the backup twice")
		}
		files[f.Name] = text
	}
	if len(files) == 0 {
		return nil, errors.New("the backup holds no file")
	}
	return files, nil
}

func readEntry(f *zip.File) (string, error) {
	switch {
	case !restorable.MatchString(f.Name):
		return "", errors.New("not the name of a settings file")
	case f.Flags&0x1 != 0:
		return "", errors.New("encrypted")
	case f.Method != zip.Store && f.Method != zip.Deflate:
		return "", errors.New("a kind of compression that is not used")
	case !f.Mode().IsRegular():
		return "", errors.New("not a plain file")
	case f.UncompressedSize64 > maxFile:
		return "", errors.New("larger than a settings file")
	}
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }() // read only
	b, err := io.ReadAll(io.LimitReader(rc, maxFile+1))
	switch {
	case err != nil:
		return "", err
	case len(b) > maxFile:
		return "", errors.New("larger than a settings file")
	case !utf8.Valid(b) || slices.Contains(b, 0) || slices.Contains(b, '\r'):
		return "", errors.New("not text with Unix line ends")
	}
	return string(b), nil
}

// mergeCurrent makes the text to restore: the saved text, with the credentials (and the URLs with credentials) of the file that is
// there now put back in their statements; a credential that the copy holds with a value is refused.
func mergeCurrent(saved, current string) (string, error) {
	sa, err := Assignments(saved)
	if err != nil {
		return "", fmt.Errorf("the saved file is unreadable: %w", err)
	}
	secrets := model.SecretNames()
	for _, a := range sa {
		if slices.Contains(secrets, a.Name) && a.Value != "" {
			return "", fmt.Errorf("%s has a value in the backup: a backup holds no credentials, so this is not one of ours", a.Name)
		}
	}
	ca, err := Assignments(current)
	if err != nil {
		return "", fmt.Errorf("the file is unreadable: %w", err)
	}
	text, err := putCurrentSecrets(saved, current, ca, secrets)
	if err != nil {
		return "", err
	}
	// a credential the copy has empty and the file does not have: take the empty statement out
	sa, _ = Assignments(text)
	for _, a := range sa {
		if slices.Contains(secrets, a.Name) && a.Value == "" && !hasName(ca, a.Name) {
			if next, err := removeAssignment(text, a.Name); err == nil {
				text = next
			}
		}
	}
	return text, nil
}

// putCurrentSecrets puts the statements of the credentials (and of the URLs with credentials) of the file that is there now into
// the saved text.
func putCurrentSecrets(text, current string, ca []Assignment, secrets []string) (string, error) {
	for _, a := range ca {
		if !slices.Contains(secrets, a.Name) && !userinfoURL(a.Value) {
			continue
		}
		cur, _, err := only(ca, a.Name)
		if err != nil {
			return "", err
		}
		if text, err = putStatement(text, a.Name, current[cur.Start:cur.End]); err != nil {
			return "", err
		}
	}
	return text, nil
}

// putStatement puts the statement as it stands in the file that is there now in place of the statement of the name in text (or at its
// end), so that a credential that the file takes from a variable ($MYTOKEN) stays what it was.
func putStatement(text, name, stmt string) (string, error) {
	as, err := Assignments(text)
	if err != nil {
		return "", err
	}
	a, found, err := only(as, name)
	if err != nil {
		return "", err
	}
	if found {
		return text[:a.Start] + stmt + text[a.End:], nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + stmt + "\n", nil
}

func hasName(as []Assignment, name string) bool {
	return slices.ContainsFunc(as, func(a Assignment) bool { return a.Name == name })
}

// diffLines says what restoring does to a file, by name (values only for the settings a page may show).
func diffLines(before, after string) []string {
	ba, _ := Assignments(before)
	aa, _ := Assignments(after)
	bv, av := assignmentValues(ba), assignmentValues(aa)
	var out []string
	for _, n := range sortedNames(bv, av) {
		if l := diffLine(n, bv, av, opaque(before, ba, n), opaque(after, aa, n)); l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 && before != after {
		out = append(out, "~ comments or the layout of the file")
	}
	return out
}

func assignmentValues(as []Assignment) map[string]string {
	m := map[string]string{}
	for _, a := range as {
		m[a.Name] = a.Value
	}
	return m
}

func sortedNames(maps ...map[string]string) []string {
	set := map[string]bool{}
	for _, m := range maps {
		for n := range m {
			set[n] = true
		}
	}
	return slices.Sorted(func(yield func(string) bool) {
		for n := range set {
			if !yield(n) {
				return
			}
		}
	})
}

// opaque says whether the file takes the value of a name from a variable or a ~: the page would not say what the shell reads.
func opaque(text string, as []Assignment, name string) bool {
	for _, a := range as {
		if a.Name == name && expands(text[a.Start:a.End]) {
			return true
		}
	}
	return false
}

// diffLine is the line for one name ("" if nothing differs).
func diffLine(n string, bv, av map[string]string, opaqueBefore, opaqueAfter bool) string {
	o, inB := bv[n]
	v, inA := av[n]
	_, inCatalog := specOf(n)
	switch {
	case (inB && opaqueBefore) || (inA && opaqueAfter):
		if o != v || inB != inA {
			return "~ " + n + ": changed"
		}
	case inA && !inB:
		return describe(kindOf(inCatalog), n, v, "", false)
	case inB && !inA:
		return "- " + n + ": removed"
	case o != v:
		return describe(kindOf(inCatalog), n, v, o, true)
	}
	return ""
}

// kindOf is the kind of description: values for the settings of the catalog, names only for the rest (and the credentials).
func kindOf(inCatalog bool) string {
	if inCatalog {
		return "setting"
	}
	return "credential"
}

// planRestore reads the backup and the files that are there, and says what each would become.
func planRestore(dir string, ch settings.Change, env settings.Env) ([]credPlan, []string) {
	saved, err := readBackup(env, ch.Name)
	if err != nil {
		return nil, []string{err.Error()}
	}
	rels := slices.Sorted(func(yield func(string) bool) {
		for r := range saved {
			if !yield(r) {
				return
			}
		}
	})
	var plans []credPlan
	var problems []string
	for _, rel := range rels {
		pl, problem := planFile(dir, rel, saved[rel])
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		plans = append(plans, pl)
	}
	return plans, problems
}

// planFile says what restoring the saved text of one file makes of the file that is there now ("" is the problem if nothing).
func planFile(dir, rel, saved string) (credPlan, string) {
	pl := credPlan{rel: rel, path: filepath.Join(dir, filepath.FromSlash(rel))}
	if err := plainFile(dir, filepath.FromSlash(rel)); err != nil {
		return pl, err.Error()
	}
	b, err := os.ReadFile(pl.path)
	if err != nil {
		return pl, err.Error()
	}
	pl.before, pl.etag = string(b), etagOf(b)
	if pl.after, err = mergeCurrent(saved, pl.before); err != nil {
		return pl, rel + ": " + err.Error()
	}
	if pl.after == pl.before {
		return pl, rel + " is the same as in the backup"
	}
	pl.lines = diffLines(pl.before, pl.after)
	for _, l := range pl.lines {
		if f := strings.Fields(l); len(f) > 1 && f[1] != "comments" {
			pl.names = append(pl.names, strings.TrimSuffix(f[1], ":"))
		}
	}
	return pl, ""
}

func etagOfPlans(plans []credPlan) string {
	var b strings.Builder
	for _, pl := range plans {
		b.WriteString(pl.rel + "=" + pl.etag + "\n")
	}
	return etagOf([]byte(b.String()))
}

// changeOf is the change of settings one restored file amounts to, for the checks that a change of settings has (a server, a
// dangerous value).
func changeOf(pl credPlan) settings.Change {
	ch := settings.Change{Kind: "setting", Target: "base", Fields: map[string]string{}}
	switch {
	case strings.HasPrefix(pl.rel, "machines/"):
		ch.Target, ch.Name = "machine", strings.TrimSuffix(strings.TrimPrefix(pl.rel, "machines/"), ".env.local")
	case strings.HasPrefix(pl.rel, "groups/"):
		ch.Target, ch.Name = "group", strings.TrimSuffix(strings.TrimPrefix(pl.rel, "groups/"), ".env")
	}
	ba, _ := Assignments(pl.before)
	aa, _ := Assignments(pl.after)
	for _, a := range aa {
		if _, ok := specOf(a.Name); ok && !slices.ContainsFunc(ba, func(b Assignment) bool { return b.Name == a.Name && b.Value == a.Value }) {
			ch.Fields[a.Name] = a.Value
		}
	}
	for _, b := range ba {
		if _, ok := specOf(b.Name); ok && !hasName(aa, b.Name) {
			ch.Remove = append(ch.Remove, b.Name)
		}
	}
	return ch
}

func previewRestore(dir string, ch settings.Change, env settings.Env) (settings.Preview, []credPlan) {
	var p settings.Preview
	plans, problems := planRestore(dir, ch, env)
	p.Problems = problems
	if len(problems) > 0 || len(plans) == 0 {
		return p, plans
	}
	p.Etag = etagOfPlans(plans)
	files := map[string]string{}
	for _, pl := range plans {
		p.Files = append(p.Files, settings.File{Path: filepath.ToSlash(pl.rel), Text: strings.Join(pl.lines, "\n") + "\n"})
		files[pl.rel] = pl.after
		sub := changeOf(pl)
		if len(sub.Fields)+len(sub.Remove) > 0 {
			checkSettingChange(dir, sub, env, &p)
		}
	}
	p.Notes = append(p.Notes, "the credentials of the files that are there now stay: a backup holds none")
	if len(p.Confirm) > 0 {
		p.ConfirmWord = "restore"
	}
	problems2, broken := diffStatus(dir, env, files, "")
	p.Problems = append(p.Problems, problems2...)
	sort.Strings(broken)
	p.Broken = broken
	return p, plans
}

func createRestore(dir string, ch settings.Change, env settings.Env) (settings.Preview, error) {
	p, plans := previewRestore(dir, ch, env)
	if len(p.Problems) > 0 || len(p.Broken) > 0 {
		return p, errors.New("the change is refused")
	}
	if p.ConfirmWord != "" && ch.Confirm != p.ConfirmWord {
		p.Problems = append(p.Problems, "type "+p.ConfirmWord+" to confirm")
		return p, errors.New("not confirmed")
	}
	if ch.Etag == "" || ch.Etag != p.Etag {
		p.Problems = append(p.Problems, "a file has changed since the preview: look at the preview again")
		return p, errors.New("a file has changed")
	}
	for _, pl := range plans { // right before anything is made
		if cur, err := os.ReadFile(pl.path); err != nil || etagOf(cur) != pl.etag {
			p.Problems = append(p.Problems, "a file has changed since the preview: look at the preview again")
			return p, errors.New("a file has changed")
		}
	}
	name, err := writeBackup(env.Home, "restore", plans)
	if err != nil {
		p.Problems = append(p.Problems, "no backup could be made: "+err.Error())
		return p, errors.New("no backup")
	}
	p.Backup = name
	for _, pl := range plans {
		if err := replaceFile(pl.path, pl.after); err != nil {
			return p, fmt.Errorf("%s: %w (the files before it were restored; the copy %s holds all of them as they were)", pl.rel, err, name)
		}
	}
	return p, nil
}
