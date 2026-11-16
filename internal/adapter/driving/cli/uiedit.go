package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// uiEditor is what viper ui --edit does for the pages (docs/plan/ui-bearbeiten.md, stage A): it hands a form to the settings
// editor, which looks at what the new file does to every machine and group, and writes only a file that is not there yet.
type uiEditor struct{ b *uiBackend }

// Templates are the providers with their defaults, the machines of the directory and whether .env.local is there.
func (e *uiEditor) Templates(ctx context.Context) (web.Templates, error) {
	t := web.Templates{Providers: []web.Template{}, Machines: append([]string{}, listNames(filepath.Join(e.b.dir(), "machines"), ".env.local")...)}
	for _, name := range model.ProviderNames() {
		if info, ok := model.LookupProvider(name); ok {
			t.Providers = append(t.Providers, web.Template{Name: name, Type: info.Size, Location: info.Region, Container: info.Container})
		}
	}
	t.Base = exists(filepath.Join(e.b.dir(), ".env.local"))
	t.Credentials = model.SecretNames()
	t.Catalog = []web.SettingInfo{}
	for _, s := range e.b.o.host().Services.SettingsEditor.Catalog() {
		t.Catalog = append(t.Catalog, web.SettingInfo{Name: s.Name, Type: s.Type, Options: s.Options, Min: s.Min, Max: s.Max, Danger: s.Danger, Help: s.Help})
	}
	t.Files = []web.CredentialFile{}
	files, err := e.b.Settings(ctx)
	if err != nil {
		return t, err
	}
	for _, f := range files {
		if cf, ok := credentialFile(f); ok {
			t.Files = append(t.Files, cf)
		}
	}
	return t, ctx.Err()
}

// credentialFile is a settings file that a credential can be set in, with the credentials it sets (names).
func credentialFile(f web.SettingsFile) (web.CredentialFile, bool) {
	cf := web.CredentialFile{Path: f.Name, Set: []string{}}
	switch {
	case f.Name == ".env.local":
		cf.Target = "base"
	case strings.HasPrefix(f.Name, "machines/") && strings.HasSuffix(f.Name, ".env.local"):
		cf.Target, cf.Name = "machine", strings.TrimSuffix(strings.TrimPrefix(f.Name, "machines/"), ".env.local")
	case strings.HasPrefix(f.Name, "groups/") && strings.HasSuffix(f.Name, ".env"):
		cf.Target, cf.Name = "group", strings.TrimSuffix(strings.TrimPrefix(f.Name, "groups/"), ".env")
	default:
		return cf, false
	}
	secrets := model.SecretNames()
	for _, s := range f.Settings {
		if s.Set && slices.Contains(secrets, s.Key) {
			cf.Set = append(cf.Set, s.Key)
		}
	}
	return cf, true
}

func change(req web.EditRequest) settings.Change {
	return settings.Change{Kind: req.Kind, Name: req.Name, Fields: req.Fields, Members: req.Members, Target: req.Target, Remove: req.Remove, Etag: req.Etag, Confirm: req.Confirm}
}

// learn gives the redaction the values of a request before anything is done with them, so that no answer can hold one.
func (e *uiEditor) learn(req web.EditRequest) {
	if req.Kind == "credential" {
		for _, v := range req.Fields {
			e.b.secrets.add(v)
		}
	}
}

func result(p settings.Preview) web.EditResult {
	r := web.EditResult{Problems: p.Problems, Broken: p.Broken, Notes: p.Notes, Etag: p.Etag, Backup: p.Backup, Confirm: p.Confirm, ConfirmWord: p.ConfirmWord}
	for _, f := range p.Files {
		r.Files = append(r.Files, web.EditFile{Path: f.Path, Text: f.Text})
	}
	return r
}

func (e *uiEditor) Preview(ctx context.Context, req web.EditRequest) (web.EditResult, error) {
	e.learn(req)
	p, err := e.b.o.host().Services.SettingsEditor.Preview(e.b.dir(), change(req), e.b.env)
	if err != nil {
		return web.EditResult{}, err
	}
	return result(p), ctx.Err()
}

// Create makes the files and leaves a line in events.log (what, never a value).
func (e *uiEditor) Create(_ context.Context, req web.EditRequest) (web.EditResult, error) {
	e.learn(req)
	p, err := e.b.o.host().Services.SettingsEditor.Create(e.b.dir(), change(req), e.b.env)
	r := result(p)
	if err == nil {
		paths := make([]string, 0, len(p.Files))
		for _, f := range p.Files {
			paths = append(paths, f.Path)
		}
		switch req.Kind {
		case "restore":
			e.trace("restored " + strings.Join(paths, ", ") + " from " + req.Name + " (backup " + p.Backup + ")")
		case "credential", "setting":
			e.trace(traceChange(req, paths, p.Backup))
		default:
			e.trace("made " + strings.Join(paths, ", "))
		}
	}
	return r, err // a request that was cancelled after the write still says what was made
}

// trace is one line of events.log, in the form the runs write: the time, the server ("ui"), the action and the text.
func (e *uiEditor) trace(text string) { e.traceAs("edit", text) }

// traceAs is one line of events.log for an action of the pages
func (e *uiEditor) traceAs(action, text string) {
	dir := model.StateDir(e.b.env.Home)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }() // best effort, like the log of a run
	fmt.Fprintf(f, "%s %-24s %-9s %s\n", time.Now().Format("2006-01-02T15:04:05-07:00"), "ui", action, text)
}

// traceChange says which settings changed in which file, and where the copy is: the names, never a value.
func traceChange(req web.EditRequest, paths []string, backup string) string {
	names := make([]string, 0, len(req.Fields)+len(req.Remove))
	for n := range req.Fields {
		names = append(names, n)
	}
	slices.Sort(names)
	text := "set " + strings.Join(names, ", ")
	if req.Kind == "setting" {
		text = "changed " + strings.Join(names, ", ")
	}
	if len(req.Remove) > 0 {
		text += "; removed " + strings.Join(req.Remove, ", ")
	}
	return text + " in " + strings.Join(paths, ", ") + " (backup " + backup + ")"
}

var validName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// copyFields are the settings of a file that a form takes over (the ones the forms of docs/plan/ui-bearbeiten.md know).
func copyFields(kind string) []string {
	if kind == "group" {
		return []string{"NATS_ENABLED"}
	}
	return []string{"PROVIDER", "DNS_PROVIDER", "SERVER_TYPE", "SERVER_LOCATION", "COMPOSE_IMAGE", "K8S_IMAGE"}
}

// Duplicate reads the file of a machine or group that exists and gives what a copy starts from: the settings the form knows (only
// those that are on the list of values that are shown, so never a credential), a free name, and the names of the rest.
func (e *uiEditor) Duplicate(ctx context.Context, kind, name string) (web.Copy, error) {
	dir, suffix := "machines", ".env.local"
	if kind == "group" {
		dir, suffix = "groups", ".env"
	} else if kind != "machine" {
		return web.Copy{}, errors.New("a machine or a group")
	}
	names := listNames(filepath.Join(e.b.dir(), dir), suffix)
	if !slices.Contains(names, name) || !validName.MatchString(name) { // the name is one of the files, never a path
		return web.Copy{}, fmt.Errorf("there is no %s %s", kind, name)
	}
	file := filepath.Join(e.b.dir(), dir, name+suffix)
	if st, err := os.Lstat(file); err != nil || !st.Mode().IsRegular() { // a link is not followed
		return web.Copy{}, fmt.Errorf("%s %s is not a plain file", kind, name)
	}
	settings, err := readSettings(file)
	if err != nil {
		return web.Copy{}, err
	}
	c := web.Copy{Kind: kind, Fields: map[string]string{}, Skipped: []string{}}
	for _, s := range settings {
		switch {
		case slices.Contains(copyFields(kind), s.Key) && s.Shown:
			c.Fields[s.Key] = s.Value
		case !slices.Contains(c.Skipped, s.Key):
			c.Skipped = append(c.Skipped, s.Key)
		}
	}
	c.Name = name + "-2"
	for i := 3; slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, c.Name) }); i++ { // a computer that ignores the case takes them for one
		c.Name = fmt.Sprintf("%s-%d", name, i)
	}
	return c, ctx.Err()
}

func (e *uiEditor) Backups(context.Context) ([]web.BackupInfo, error) {
	list, err := e.b.o.host().Services.SettingsEditor.Backups(e.b.env)
	out := make([]web.BackupInfo, 0, len(list))
	for _, b := range list {
		out = append(out, web.BackupInfo{Name: b.Name, Time: b.Time, Files: b.Files})
	}
	return out, err
}

func (e *uiEditor) OpenBackup(_ context.Context, name string) (io.ReadCloser, error) {
	return e.b.o.host().Services.SettingsEditor.OpenBackup(e.b.env, name)
}

func (e *uiEditor) ReadSetting(_ context.Context, target, name, setting string) (string, error) {
	return e.b.o.host().Services.SettingsEditor.ReadSetting(e.b.dir(), target, name, setting, e.b.env)
}
