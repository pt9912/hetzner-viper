package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	t := web.Templates{Providers: []web.Template{}, Machines: listNames(filepath.Join(e.b.dir(), "machines"), ".env.local")}
	for _, name := range model.ProviderNames() {
		if info, ok := model.LookupProvider(name); ok {
			t.Providers = append(t.Providers, web.Template{Name: name, Type: info.Size, Location: info.Region, Container: info.Container})
		}
	}
	t.Base = exists(filepath.Join(e.b.dir(), ".env.local"))
	t.Credentials = model.SecretNames()
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
	return settings.Change{Kind: req.Kind, Name: req.Name, Fields: req.Fields, Members: req.Members, Target: req.Target, Remove: req.Remove, Etag: req.Etag}
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
	r := web.EditResult{Problems: p.Problems, Broken: p.Broken, Notes: p.Notes, Etag: p.Etag, Backup: p.Backup}
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
		if req.Kind == "credential" {
			e.trace(traceCredentials(req, paths, p.Backup))
		} else {
			e.trace("made " + strings.Join(paths, ", "))
		}
	}
	return r, err // a request that was cancelled after the write still says what was made
}

// trace is one line of events.log, in the form the runs write: the time, the server ("ui"), the action and the text.
func (e *uiEditor) trace(text string) {
	dir := model.StateDir(e.b.env.Home)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }() // best effort, like the log of a run
	fmt.Fprintf(f, "%s %-24s %-9s %s\n", time.Now().Format("2006-01-02T15:04:05-07:00"), "ui", "edit", text)
}

// traceCredentials says which credentials changed in which file, and where the copy is: the names, never a value.
func traceCredentials(req web.EditRequest, paths []string, backup string) string {
	names := make([]string, 0, len(req.Fields)+len(req.Remove))
	for n := range req.Fields {
		names = append(names, n)
	}
	slices.Sort(names)
	text := "set " + strings.Join(names, ", ")
	if len(req.Remove) > 0 {
		text += "; removed " + strings.Join(req.Remove, ", ")
	}
	return text + " in " + strings.Join(paths, ", ") + " (backup " + backup + ")"
}
