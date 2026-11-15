package web

import (
	"context"
	"encoding/json"
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
}

// Editor is what makes the new files. The cli package gives it.
type Editor interface {
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
	out := EditResult{Files: []EditFile{}, Problems: []string{}, Broken: []string{}, Notes: []string{}, Done: res.Done, Etag: res.Etag, Backup: s.show(res.Backup)}
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
	return out
}

// runActive says whether a run of an action is going: a credential is not changed under it (it read the files at its start, and
// the next one should read the new ones).
func (s *Server) runActive() bool {
	t := &s.runs
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, o := range t.runs {
		if o.Action != "nats" && o.current() != stateDone {
			return true
		}
	}
	return false
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
	if req.Kind == "credential" && s.runActive() {
		fail(w, http.StatusConflict, "a run is going: wait for it or cancel it, then change the credentials")
		return
	}
	if !s.editMu.TryLock() {
		fail(w, http.StatusConflict, "another change is being made")
		return
	}
	defer s.editMu.Unlock()
	res, err := s.cfg.Editor.Create(r.Context(), req)
	s.forgetOverview()
	if err != nil && len(res.Problems) == 0 && len(res.Broken) == 0 {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	res.Done = err == nil // a refusal is an answer (200, done: false) with its reasons
	reply(w, s.showResult(res))
}
