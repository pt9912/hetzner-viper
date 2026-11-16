package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// Editing (docs/plan/ui-bearbeiten.md, stage A): new settings files, and only with viper ui --edit. The server holds no
// path and writes no file: the Editor does, and it makes only files that are not there yet.

// EditRequest is a form: the kind of file ("machine", "group" or "base"), its name, the settings and the members of a group.
type EditRequest struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Fields  map[string]string `json:"fields"`
	Members []string          `json:"members"`
	// kind "credential": the file (target "base", "machine" or "group"; name), the credentials to take out, and the state of the
	// file the preview saw. The values of the credentials are in Fields: they go in, and are never in an answer.
	Target string   `json:"target"`
	Remove []string `json:"remove"`
	Etag   string   `json:"etag"`
	// kind "setting" changes settings that are no credentials (Fields: name -> value); Confirm is the word a dangerous one asks for.
	Confirm string `json:"confirm"`
}

// EditFile is a file a request makes, as text.
type EditFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// EditResult is what a request would do (preview), or did (Done).
type EditResult struct {
	Files    []EditFile `json:"files"`
	Problems []string   `json:"problems"`
	Broken   []string   `json:"broken"`
	Notes    []string   `json:"notes"`
	Done     bool       `json:"done"`
	Etag     string     `json:"etag"`
	Backup   string     `json:"backup"`
	// a change that asks to be confirmed: the reasons, and the word to type
	Confirm     []string `json:"confirm"`
	ConfirmWord string   `json:"confirmWord"`
}

// SettingInfo is a setting that a form may change: its kind of value and the limits.
type SettingInfo struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Options []string          `json:"options"`
	Min     int               `json:"min"`
	Max     int               `json:"max"`
	Danger  bool              `json:"danger"`
	Help    map[string]string `json:"help"`
}

// BackupInfo is a backup of a change.
type BackupInfo struct {
	Name  string   `json:"name"`
	Time  string   `json:"time"`
	Files []string `json:"files"`
}

// Template is a provider with the defaults the form offers.
type Template struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Location  string `json:"location"`
	Container bool   `json:"container"`
}

// CredentialFile is a settings file that exists, with the credentials that it sets (names only).
type CredentialFile struct {
	Target string   `json:"target"` // base, machine or group
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Set    []string `json:"set"`
}

// Templates is what the forms offer: the providers, the machines that exist (for a group), whether .env.local exists, and the
// credentials with the files that set them.
type Templates struct {
	Providers   []Template       `json:"providers"`
	Machines    []string         `json:"machines"`
	Base        bool             `json:"base"`
	Credentials []string         `json:"credentials"`
	Files       []CredentialFile `json:"files"`
	Catalog     []SettingInfo    `json:"catalog"`
}

// Copy is what a form takes over from a machine or group that exists (names and values of the settings that the form knows,
// never a credential), with a free name for the copy and the names of the settings that are not taken over.
type Copy struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Fields  map[string]string `json:"fields"`
	Skipped []string          `json:"skipped"`
}

// Editor is what makes the new files. The cli package gives it.
type Editor interface {
	// Duplicate reads what a copy of the machine or group (kind "machine" or "group") starts from.
	Duplicate(ctx context.Context, kind, name string) (Copy, error)
	// ReadSetting gives a setting that is text over several lines (target base, machine or group; the name of the file's machine or group).
	ReadSetting(ctx context.Context, target, name, setting string) (string, error)
	// Backups lists the backups of changes; OpenBackup opens one by its name.
	Backups(ctx context.Context) ([]BackupInfo, error)
	OpenBackup(ctx context.Context, name string) (io.ReadCloser, error)
	Templates(ctx context.Context) (Templates, error)
	Preview(ctx context.Context, req EditRequest) (EditResult, error)
	Create(ctx context.Context, req EditRequest) (EditResult, error)
}

const maxMembers = 200

func (s *Server) editing(w http.ResponseWriter) bool {
	if s.cfg.Editor == nil || s.cfg.Readonly {
		fail(w, http.StatusForbidden, "settings are not edited (viper ui --edit)")
		return false
	}
	return true
}

func (s *Server) duplicate(w http.ResponseWriter, r *http.Request) {
	if !s.editing(w) {
		return
	}
	c, err := s.cfg.Editor.Duplicate(r.Context(), r.URL.Query().Get("kind"), r.URL.Query().Get("name"))
	if err != nil {
		fail(w, http.StatusNotFound, s.show(err.Error()))
		return
	}
	for k, v := range c.Fields {
		c.Fields[k] = s.show(v)
	}
	reply(w, c)
}

func (s *Server) templates(w http.ResponseWriter, r *http.Request) {
	if !s.editing(w) {
		return
	}
	t, err := s.cfg.Editor.Templates(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	reply(w, t)
}

// editBody reads a request and answers a bad one.
func (s *Server) editBody(w http.ResponseWriter, r *http.Request) (EditRequest, bool) {
	var req EditRequest
	if !s.editing(w) {
		return req, false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || len(req.Members) > maxMembers || len(req.Fields) > 64 {
		fail(w, http.StatusBadRequest, "bad request")
		return req, false
	}
	return req, true
}

func (s *Server) showResult(res EditResult) EditResult {
	out := EditResult{Files: []EditFile{}, Problems: []string{}, Broken: []string{}, Notes: []string{}, Done: res.Done, Etag: res.Etag, Backup: s.show(res.Backup), Confirm: []string{}, ConfirmWord: s.show(res.ConfirmWord)}
	for _, f := range res.Files {
		out.Files = append(out.Files, EditFile{Path: s.show(f.Path), Text: s.cfg.Redact(f.Text)}) // the text is a file: its line ends stay
	}
	for _, p := range res.Problems {
		out.Problems = append(out.Problems, s.show(p))
	}
	for _, p := range res.Broken {
		out.Broken = append(out.Broken, s.show(p))
	}
	for _, p := range res.Notes {
		out.Notes = append(out.Notes, s.show(p))
	}
	for _, p := range res.Confirm {
		out.Confirm = append(out.Confirm, s.show(p))
	}
	return out
}

// beginEdit says whether a credential may be changed now: no run of an action goes (it read the files at its start, and the
// next one should read the new ones), and none is started until endEdit (the check and the write are one step for runs).
func (s *Server) beginEdit() bool {
	t := &s.runs
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, o := range t.runs {
		if !isSide(o.Action) && o.current() != stateDone {
			return false
		}
	}
	t.editing = true
	return true
}

func (s *Server) endEdit() {
	s.runs.mu.Lock()
	s.runs.editing = false
	s.runs.mu.Unlock()
}

func (s *Server) editPreview(w http.ResponseWriter, r *http.Request) {
	req, ok := s.editBody(w, r)
	if !ok {
		return
	}
	res, err := s.cfg.Editor.Preview(r.Context(), req)
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	reply(w, s.showResult(res))
}

// editApply makes the files. One at a time: the preview and the writing of a second request would otherwise look at the same
// directory.
func (s *Server) editApply(w http.ResponseWriter, r *http.Request) {
	req, ok := s.editBody(w, r)
	if !ok {
		return
	}
	if !s.editMu.TryLock() {
		fail(w, http.StatusConflict, "another change is being made")
		return
	}
	defer s.editMu.Unlock()
	if req.Kind == "credential" || req.Kind == "setting" || req.Kind == "restore" {
		if !s.beginEdit() {
			fail(w, http.StatusConflict, "a run is going: wait for it or cancel it, then change the settings")
			return
		}
		defer s.endEdit()
	}
	res, err := s.cfg.Editor.Create(r.Context(), req)
	s.forgetOverview()
	if err != nil && len(res.Problems) == 0 && len(res.Broken) == 0 {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	res.Done = err == nil // a refusal is an answer (200, done: false) with its reasons
	reply(w, s.showResult(res))
}

func (s *Server) backups(w http.ResponseWriter, r *http.Request) {
	if !s.editing(w) {
		return
	}
	list, err := s.cfg.Editor.Backups(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	reply(w, map[string][]BackupInfo{"backups": list})
}

// backup gives a backup as a download: the ZIP, never shown as a page (it holds the files without their credentials).
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	if !s.editing(w) {
		return
	}
	rc, err := s.cfg.Editor.OpenBackup(r.Context(), r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "no such backup")
		return
	}
	defer func() { _ = rc.Close() }() // read only
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("name")+`"`)
	_, _ = io.Copy(w, rc)
}

// setting gives the commands of a machine (the text over several lines that the settings page cannot show), for the form that
// changes them. Only for the settings of that kind; the text goes through the redaction like any other.
func (s *Server) setting(w http.ResponseWriter, r *http.Request) {
	if !s.editing(w) {
		return
	}
	q := r.URL.Query()
	v, err := s.cfg.Editor.ReadSetting(r.Context(), q.Get("target"), q.Get("name"), q.Get("setting"))
	if err != nil {
		fail(w, http.StatusNotFound, s.show(err.Error()))
		return
	}
	reply(w, map[string]string{"value": s.cfg.Redact(v)}) // line breaks stay: it is text for a field
}
