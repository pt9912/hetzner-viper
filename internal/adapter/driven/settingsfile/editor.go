package settingsfile

import (
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

	"github.com/pt9912/hetzner-viper/internal/filelock"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// The editor makes new settings files (docs/plan/ui-bearbeiten.md, stage A). It writes only what viper would read back, only
// the settings it knows, only files that are not there yet, and only after it has looked at what the files would do to every
// machine and group of the directory.

const maxValue = 256 // a setting of the forms: a name, a type, a domain

// reservedNames cannot be the name of a machine or group: the NATS operator, and the names Windows keeps for devices (they are
// refused on every system, so that a directory can be moved).
func reservedName(name string) bool {
	if name == model.NATSAdminName || strings.HasPrefix(name, "example") {
		return true
	}
	if slices.Contains([]string{"con", "prn", "aux", "nul"}, name) {
		return true
	}
	for _, p := range []string{"com", "lpt"} {
		if len(name) == 4 && strings.HasPrefix(name, p) && name[3] >= '1' && name[3] <= '9' {
			return true
		}
	}
	return false
}

func checkName(kind, name string) error {
	if !nameRe.MatchString(name) {
		return errors.New("the name has lowercase letters, digits and hyphens (not at the ends)")
	}
	if reservedName(name) {
		return errors.New("this name is reserved")
	}
	if kind == "group" {
		return model.CheckNATSName("group name", name)
	}
	if len(name) > 31 {
		return errors.New("the name has at most 31 characters")
	}
	return nil
}

func checkText(v string) error {
	if len(v) > maxValue {
		return fmt.Errorf("longer than %d characters", maxValue)
	}
	if !utf8.ValidString(v) {
		return errors.New("not text")
	}
	if strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || (r >= 0x7f && r <= 0x9f) }) {
		return errors.New("no line break or control character")
	}
	return nil
}

func oneOf(list ...string) func(string) error {
	return func(v string) error {
		if !slices.Contains(list, v) {
			return fmt.Errorf("one of %s", strings.Join(list, ", "))
		}
		return nil
	}
}

type field struct {
	name  string
	check func(string) error
}

// fieldsOf are the settings a kind of file may get, in the order they are written.
func fieldsOf(kind string) []field {
	text := checkText
	switch kind {
	case "machine":
		return []field{{"PROVIDER", oneOf(model.ProviderNames()...)}, {"DNS_PROVIDER", oneOf("cloudflare", "none")}, {"SERVER_TYPE", text}, {"SERVER_LOCATION", text}, {"SERVER_IMAGE", text}, {"COMPOSE_IMAGE", text}, {"K8S_IMAGE", text}}
	case "group":
		return []field{{"NATS_ENABLED", oneOf("true", "false")}, {"NATS_MCP", oneOf("true", "false")}, {"NATS_OBJECTS", oneOf("true", "false")}}
	case "base":
		return []field{{"SERVER_PREFIX", func(v string) error {
			if !nameRe.MatchString(v) {
				return errors.New("lowercase letters, digits and hyphens")
			}
			return nil
		}}, {"PROVIDER", oneOf(model.ProviderNames()...)}, {"DNS_PROVIDER", oneOf("cloudflare", "none")}, {"DNS_DOMAIN", text}}
	}
	return nil
}

// quote writes a value so that bash and Source read it back as it is: in single quotes, a quote as '\”.
func quote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

// render is the file a change makes, and the reasons it cannot.
func render(ch settings.Change) (path, text string, problems []string) {
	fields := fieldsOf(ch.Kind)
	if fields == nil {
		return "", "", []string{"unknown kind of file"}
	}
	switch ch.Kind {
	case "machine":
		path = filepath.Join("machines", ch.Name+".env.local")
	case "group":
		path = filepath.Join("groups", ch.Name+".env")
	default:
		path = ".env.local"
	}
	if ch.Kind != "base" {
		if err := checkName(ch.Kind, ch.Name); err != nil {
			problems = append(problems, "name: "+err.Error())
		}
	}
	vals, more := checkFields(ch, fields)
	problems = append(problems, more...)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: made by viper ui. Settings that are not here have their defaults (see .env.local.example).\n", filepath.ToSlash(path))
	if ch.Kind == "group" {
		members, err := membersText(ch.Members)
		if err != nil {
			problems = append(problems, "GROUP_MACHINES: "+err.Error())
		} else {
			b.WriteString("GROUP_MACHINES=" + quote(members) + "\n")
		}
	}
	if info, ok := model.LookupProvider(vals["PROVIDER"]); ch.Kind == "machine" && ok && info.Container && vals["DNS_PROVIDER"] == "" {
		vals["DNS_PROVIDER"] = "none" // a container has no DNS name
	}
	for _, f := range fields {
		if v, ok := vals[f.name]; ok {
			b.WriteString(f.name + "=" + quote(v) + "\n")
		}
	}
	return path, b.String(), problems
}

// checkFields are the settings of a change that the file may have, checked; the others are reasons to refuse it.
func checkFields(ch settings.Change, fields []field) (vals map[string]string, problems []string) {
	known := map[string]bool{}
	for _, f := range fields {
		known[f.name] = true
	}
	for k := range ch.Fields {
		if !known[k] {
			problems = append(problems, k+": not a setting of this file")
		}
	}
	vals = map[string]string{}
	for _, f := range fields {
		v := strings.TrimSpace(ch.Fields[f.name])
		if v == "" {
			continue
		}
		if err := f.check(v); err != nil {
			problems = append(problems, f.name+": "+err.Error())
			continue
		}
		vals[f.name] = v
	}
	return vals, problems
}

func membersText(members []string) (string, error) {
	if len(members) == 0 {
		return "", errors.New("name at least one machine")
	}
	seen := map[string]bool{}
	for _, m := range members {
		if !nameRe.MatchString(m) || seen[m] {
			return "", fmt.Errorf("%q is not a machine name, or it is named twice", m)
		}
		seen[m] = true
	}
	return strings.Join(members, ","), nil
}

// overlay is the disk with some files as they would be.
type overlay struct {
	files map[string][]byte
	base  settings.FS
}

func (o overlay) ReadFile(path string) ([]byte, error) {
	if b, ok := o.files[filepath.Clean(path)]; ok {
		return b, nil
	}
	if o.base != nil {
		return o.base.ReadFile(path)
	}
	return os.ReadFile(path)
}

type namedEntry string

func (e namedEntry) Name() string             { return string(e) }
func (namedEntry) IsDir() bool                { return false }
func (namedEntry) Type() fs.FileMode          { return 0 }
func (namedEntry) Info() (fs.FileInfo, error) { return nil, errors.New("not on disk yet") }

func (o overlay) ReadDir(path string) ([]fs.DirEntry, error) {
	var ents []fs.DirEntry
	var err error
	if o.base != nil {
		ents, err = o.base.ReadDir(path)
	} else {
		ents, err = os.ReadDir(path)
	}
	have := map[string]bool{}
	for _, e := range ents {
		have[e.Name()] = true
	}
	added := false
	for p := range o.files {
		if filepath.Dir(p) == filepath.Clean(path) && !have[filepath.Base(p)] {
			ents = append(ents, namedEntry(filepath.Base(p)))
			added = true
		}
	}
	if added {
		err = nil // a directory that is made by the change
	}
	return ents, err
}

// status loads every machine and group of the directory and says what is wrong with each (no entry: it loads).
func status(dir string, env settings.Env) map[string]string {
	out := map[string]string{}
	ents, _ := readDir(env, filepath.Join(dir, "machines"))
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".env.local")
		if !ok || e.IsDir() || n == "" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "example") {
			continue
		}
		out["machine "+n] = ""
		if _, err := load(dir, n, env, false); err != nil {
			out["machine "+n] = err.Error()
		}
	}
	for _, n := range groupFiles(dir, env) {
		e := env
		e.Group = n
		out["group "+n] = ""
		g, err := LoadGroup(dir, n, e)
		if err != nil {
			out["group "+n] = err.Error()
			continue
		}
		for _, m := range g.Machines { // a machine that only a group names is a machine of the overview as well
			if _, seen := out["machine "+m]; !seen {
				out["machine "+m] = ""
				if _, err := load(dir, m, e, false); err != nil {
					out["machine "+m] = err.Error()
				}
			}
		}
	}
	return out
}

// refuseTarget says why a file cannot be made: it is there (in any spelling), or its directory is a link.
func refuseTarget(dir, rel string) string {
	full := filepath.Join(dir, rel)
	if d := filepath.Dir(full); d != filepath.Clean(dir) {
		if st, err := os.Lstat(d); err == nil && (!st.IsDir() || st.Mode()&fs.ModeSymlink != 0) {
			return filepath.ToSlash(filepath.Dir(rel)) + " is a link or no directory: change it at the terminal"
		}
	}
	if _, err := os.Lstat(full); err == nil {
		return filepath.ToSlash(rel) + " exists already"
	}
	ents, _ := os.ReadDir(filepath.Dir(full))
	for _, e := range ents {
		if strings.EqualFold(e.Name(), filepath.Base(rel)) {
			return filepath.ToSlash(rel) + " exists already (" + e.Name() + ": a computer that ignores the case takes them for one file)"
		}
	}
	return ""
}

// missingMembers are the machines of a group that have no file.
func missingMembers(dir string, ch settings.Change) (out []string) {
	if ch.Kind != "group" {
		return nil
	}
	for _, m := range ch.Members {
		if !nameRe.MatchString(m) {
			out = append(out, "GROUP_MACHINES: "+m+" is not a machine name")
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, "machines", m+".env.local")); err != nil || !st.Mode().IsRegular() { // a link to a file is a machine, as for load
			out = append(out, "GROUP_MACHINES: there is no machine "+m)
		}
	}
	return out
}

// diffStatus loads every machine and group with the file path as text says (in memory) and compares it with what loads now: a
// message about the thing the file is (key) is a problem, one about anything else that loaded before or fails differently is
// something the change breaks.
// withFile is env with one settings file as the text says (in memory).
func withFile(dir string, env settings.Env, path, text string) settings.Env {
	return withFiles(dir, env, map[string]string{path: text})
}

// withFiles is env with several settings files as the texts say (in memory).
func withFiles(dir string, env settings.Env, files map[string]string) settings.Env {
	o := overlay{files: map[string][]byte{}, base: env.Files}
	for p, t := range files {
		o.files[filepath.Clean(filepath.Join(dir, p))] = []byte(t)
	}
	env.Files = o
	return env
}

func diffStatus(dir string, env settings.Env, files map[string]string, key string) (problems, broken []string) {
	before := status(dir, env)
	for k, msg := range status(dir, withFiles(dir, env, files)) {
		if msg == "" {
			continue
		}
		prev, existed := before[k]
		switch {
		case k == key:
			problems = append(problems, msg)
		case !existed || prev != msg: // a machine that failed before and fails differently now is broken by this change
			broken = append(broken, k+": "+msg)
		}
	}
	return problems, broken
}

// Preview says what the change would do, and writes nothing.
func (Files) Preview(dir string, ch settings.Change, env settings.Env) (settings.Preview, error) {
	if ch.Kind == "restore" {
		p, _ := previewRestore(dir, ch, env)
		return p, nil
	}
	if ch.Kind == "credential" || ch.Kind == "setting" {
		return previewCredential(dir, ch, env)
	}
	var p settings.Preview
	path, text, problems := render(ch)
	p.Problems = problems
	if path == "" {
		return p, nil
	}
	p.Files = []settings.File{{Path: filepath.ToSlash(path), Text: text}}
	if msg := refuseTarget(dir, path); msg != "" {
		p.Problems = append(p.Problems, msg)
	}
	p.Problems = append(p.Problems, missingMembers(dir, ch)...)
	if len(p.Problems) > 0 {
		return p, nil
	}
	key := ch.Kind + " " + ch.Name
	problems2, broken := diffStatus(dir, env, map[string]string{path: text}, key)
	p.Problems = append(p.Problems, problems2...)
	p.Broken = append(p.Broken, broken...)
	sort.Strings(p.Broken)
	if ch.Kind == "machine" && len(p.Problems) == 0 {
		if _, err := load(dir, ch.Name, withFile(dir, env, path, text), true); err != nil {
			p.Notes = append(p.Notes, err.Error())
		}
	}
	return p, nil
}

// Create makes the file of a change that Preview accepts.
func (f Files) Create(dir string, ch settings.Change, env settings.Env) (settings.Preview, error) {
	// one change at a time on this computer, also between two viper ui: the check of the file and the write are one step
	if err := os.MkdirAll(model.StateDir(env.Home), 0o700); err != nil {
		return settings.Preview{}, err
	}
	unlock, err := lockSettings(filepath.Join(model.StateDir(env.Home), "settings-edit.lock"), lockWait)
	if err != nil {
		return settings.Preview{}, err
	}
	defer unlock()
	return f.create(dir, ch, env)
}

func (f Files) create(dir string, ch settings.Change, env settings.Env) (settings.Preview, error) {
	if ch.Kind == "restore" {
		return createRestore(dir, ch, env)
	}
	if ch.Kind == "credential" || ch.Kind == "setting" {
		return createCredential(dir, ch, env)
	}
	p, err := f.Preview(dir, ch, env)
	if err != nil {
		return p, err
	}
	if len(p.Problems) > 0 || len(p.Broken) > 0 {
		return p, errors.New("the change is refused")
	}
	for _, file := range p.Files {
		if err := writeNew(filepath.Join(dir, filepath.FromSlash(file.Path)), file.Text); err != nil {
			return p, err
		}
	}
	return p, nil
}

// writeNew puts a file in place in one step and never replaces one: the text goes to a file of its own first and is linked
// to its name (that fails if the name is taken). Where links are not possible the file is made with O_EXCL.
func writeNew(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".viper-new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // the name it was linked to stays
	_, werr := tmp.WriteString(text)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o600)
	}
	if werr != nil {
		return werr
	}
	err = os.Link(tmp.Name(), path)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s exists already", filepath.Base(path))
	}
	if err == nil {
		return nil
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr = out.WriteString(text)
	if cerr := out.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path) // a half written new file is worse than none
	}
	return werr
}

// lockWait is how long a change waits for the change of another viper ui.
const lockWait = 30 * time.Second

// lockSettings takes the lock of the settings, and gives up after lockWait: a program that holds it and does not go on (a file
// system that does not answer) must not hold every other change for good. A lock that is taken after the giving up is let go at once.
func lockSettings(path string, wait time.Duration) (func(), error) {
	type result struct {
		unlock func()
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		u, err := filelock.Lock(path)
		ch <- result{u, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("cannot take the lock of the settings: %w", r.err)
		}
		return r.unlock, nil
	case <-time.After(wait):
		go func() {
			if r := <-ch; r.err == nil {
				r.unlock()
			}
		}()
		return nil, errors.New("another viper ui is changing settings (waited 30 seconds): try again")
	}
}
