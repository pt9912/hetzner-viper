package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	return t, ctx.Err()
}

func change(req web.EditRequest) settings.Change {
	return settings.Change{Kind: req.Kind, Name: req.Name, Fields: req.Fields, Members: req.Members}
}

func result(p settings.Preview) web.EditResult {
	r := web.EditResult{Problems: p.Problems, Broken: p.Broken, Notes: p.Notes}
	for _, f := range p.Files {
		r.Files = append(r.Files, web.EditFile{Path: f.Path, Text: f.Text})
	}
	return r
}

func (e *uiEditor) Preview(ctx context.Context, req web.EditRequest) (web.EditResult, error) {
	p, err := e.b.o.host().Services.SettingsEditor.Preview(e.b.dir(), change(req), e.b.env)
	if err != nil {
		return web.EditResult{}, err
	}
	return result(p), ctx.Err()
}

// Create makes the files and leaves a line in events.log (what, never a value).
func (e *uiEditor) Create(_ context.Context, req web.EditRequest) (web.EditResult, error) {
	p, err := e.b.o.host().Services.SettingsEditor.Create(e.b.dir(), change(req), e.b.env)
	r := result(p)
	if err == nil {
		paths := make([]string, 0, len(p.Files))
		for _, f := range p.Files {
			paths = append(paths, f.Path)
		}
		e.trace("made " + strings.Join(paths, ", "))
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
