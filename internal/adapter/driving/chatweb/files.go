package chatweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

// The files of the chat: what the person hands over to send waits in a folder of its own until its message goes (or too long),
// and what is fetched for a download is kept for a while (docs/plan/ui-chat.md §3.3). Nothing of it is shown in the page: a file is
// always a download.
const (
	maxFile     = 64 << 20 // the largest file of the page; the limit of the group applies as well
	maxWaiting  = 4        // files that wait for their message
	uploadLife  = 10 * time.Minute
	cacheLife   = 30 * time.Minute
	cacheBudget = 256 << 20 // the most the downloads keep
)

type upload struct {
	group, name, path, typ string
	size                   int64
	at                     time.Time
}

// uploads is the folder and the table of the files that wait to be sent, and the cache of the downloads.
type uploads struct {
	mu     sync.Mutex
	dir    string
	files  map[string]*upload
	seq    int
	closed bool
	flight map[string]*call // the downloads that are being fetched
}

// root is the folder of the chat's files (made on first use, only for the user).
func (u *uploads) root() (string, error) {
	if u.closed {
		return "", errors.New("viper ui-chat is ending")
	}
	if u.dir == "" {
		dir, err := os.MkdirTemp("", "viper-chat-")
		if err != nil {
			return "", err
		}
		u.dir, u.files = dir, map[string]*upload{}
	}
	return u.dir, nil
}

func (u *uploads) get(group, id string) *upload {
	u.mu.Lock()
	defer u.mu.Unlock()
	f := u.files[id]
	if f == nil || f.group != group || f.typ == "" || time.Since(f.at) > uploadLife { // typ: the body has arrived and was looked at
		return nil
	}
	return f
}

func (u *uploads) drop(id string) {
	u.mu.Lock()
	f := u.files[id]
	delete(u.files, id)
	dir := u.dir
	u.mu.Unlock()
	if f != nil && dir != "" {
		_ = os.RemoveAll(filepath.Dir(f.path))
	}
}

func (u *uploads) close() {
	u.mu.Lock()
	u.closed = true
	dir := u.dir
	u.mu.Unlock()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// reserve takes a place and makes the folder of the file; files that waited too long are given up first.
func (u *uploads) reserve(group, name string) (id string, f *upload, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	root, err := u.root()
	if err != nil {
		return "", nil, err
	}
	for k, x := range u.files {
		if time.Since(x.at) > uploadLife {
			delete(u.files, k)
			_ = os.RemoveAll(filepath.Dir(x.path))
		}
	}
	if len(u.files) >= maxWaiting {
		return "", nil, errors.New("too many files wait; send one first")
	}
	u.seq++
	id = "u" + strconv.Itoa(u.seq)
	sub := filepath.Join(root, "up-"+id)
	if err := os.Mkdir(sub, 0o700); err != nil {
		return "", nil, err
	}
	f = &upload{group: group, name: name, path: filepath.Join(sub, name), at: time.Now()}
	u.files[id] = f
	return id, f, nil
}

// cleanName keeps what a name of a file may hold: letters, digits and . _ - (the command checks the name again).
func cleanName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, `\`, "/"))
	var b strings.Builder
	for _, r := range n {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r)) {
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), ".-")
	if len(out) > 64 {
		out = out[len(out)-64:]
	}
	if out == "" {
		return "file"
	}
	return out
}

// upload is POST /api/chat/upload?group=dev&name=report.pdf with the file as the body: it is looked at (the type comes from its first
// bytes, as the machines see it) and kept until its message goes.
func (h *handlers) upload(w http.ResponseWriter, r *http.Request) {
	if h.o.Readonly {
		web.Fail(w, http.StatusForbidden, "this page only reads (--readonly)")
		return
	}
	g, ok := h.group(w, r)
	if !ok {
		return
	}
	switch {
	case !g.Operator:
		web.Fail(w, http.StatusConflict, "you are not logged in in this group")
		return
	case g.FileMax <= 0:
		web.Fail(w, http.StatusConflict, "this group has no object store (NATS_OBJECTS=true): files cannot be sent")
		return
	}
	limit := min(g.FileMax, maxFile)
	id, f, err := h.files.reserve(g.Name, cleanName(r.URL.Query().Get("name")))
	if err != nil {
		web.Fail(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	fail := func(code int, msg string) {
		h.files.drop(id)
		web.Fail(w, code, msg)
	}
	out, err := os.OpenFile(f.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail(http.StatusInternalServerError, "no file")
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute)) // a body that does not come ends the request
	n, err := io.Copy(out, http.MaxBytesReader(w, r.Body, limit))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		fail(http.StatusRequestEntityTooLarge, fmt.Sprintf("the file has more than %s (NATS_OBJECTS_FILE_MAX of the group)", sizeText(limit)))
		return
	case err != nil || n < 1:
		fail(http.StatusBadRequest, "the file did not arrive")
		return
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		fail(http.StatusInternalServerError, "no file")
		return
	}
	typ := natsmcp.Classify(data)
	if typ == "" {
		fail(http.StatusUnsupportedMediaType, "only PDF, ZIP and images (PNG, JPEG, GIF, WebP) are sent; the type is judged by the first bytes")
		return
	}
	if err := natsmcp.CheckAttachment(typ, data); err != nil {
		fail(http.StatusUnsupportedMediaType, err.Error())
		return
	}
	h.files.mu.Lock()
	f.typ, f.size = typ, n
	h.files.mu.Unlock()
	web.Reply(w, map[string]any{"id": id, "name": f.name, "type": typ, "size": n})
}

func sizeText(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d KiB", max(n>>10, 1))
}

// file is GET /api/chat/file?group=dev&conv=rechner__a&n=12: the file of a message that is in the history (the page cannot ask for an
// object that no message named). It is fetched once and kept for a while, and the answer is a download, never a page of this server.
func (h *handlers) file(w http.ResponseWriter, r *http.Request) {
	g, ok := h.group(w, r)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64)
	conv := r.URL.Query().Get("conv")
	if err != nil || n < 1 || conv == "" || !g.Operator {
		web.Fail(w, http.StatusBadRequest, "which message?")
		return
	}
	recs, err := h.o.Store.After(g.Name, conv, n-1, 1)
	if err != nil || len(recs) != 1 || recs[0].N != n || recs[0].AttID == "" || recs[0].AttSHA == "" {
		web.Fail(w, http.StatusNotFound, "no file")
		return
	}
	rec := recs[0]
	path, err := h.cached(r, g.Name, rec.AttID, rec.AttSHA)
	if err != nil {
		web.Fail(w, http.StatusBadGateway, err.Error())
		return
	}
	f, err := os.Open(path)
	if err != nil {
		web.Fail(w, http.StatusNotFound, "no file")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10)) // a download that breaks off is seen as one
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(cleanName(rec.AttName)))
	_, _ = io.Copy(w, f)
}

// call is a download that is being fetched; the ones that ask for the same file meanwhile wait for it.
type call struct {
	done chan struct{}
	path string
	err  error
}

// cached is the path of the fetched file, from the cache if it is there. The same file is fetched once at a time, and a fetch does not
// stop the others.
func (h *handlers) cached(r *http.Request, group, attID, sha string) (string, error) {
	sum := sha256.Sum256([]byte(group + "/" + attID + "/" + sha))
	key := hex.EncodeToString(sum[:16])
	u := &h.files
	u.mu.Lock()
	root, err := u.root()
	if err != nil {
		u.mu.Unlock()
		return "", err
	}
	dir := filepath.Join(root, "cache", key)
	if c := u.flight[key]; c != nil { // somebody fetches it already: its folder holds a part of the file until it is done
		u.mu.Unlock()
		select {
		case <-c.done:
			return c.path, c.err
		case <-r.Context().Done():
			return "", r.Context().Err()
		}
	}
	if p, ok := oneFile(dir); ok {
		u.mu.Unlock()
		_ = os.Chtimes(dir, time.Now(), time.Now())
		return p, nil
	}
	c := &call{done: make(chan struct{})}
	if u.flight == nil {
		u.flight = map[string]*call{}
	}
	u.flight[key] = c
	busy := map[string]bool{}
	for k := range u.flight {
		busy[k] = true
	}
	prune(filepath.Join(root, "cache"), busy)
	err = os.MkdirAll(dir, 0o700)
	u.mu.Unlock()
	defer func() { // whatever happens, the ones that wait are let go
		if p := recover(); p != nil {
			c.err = errors.New("the fetch failed")
			_ = os.RemoveAll(dir)
		}
		u.mu.Lock()
		delete(u.flight, key)
		u.mu.Unlock()
		close(c.done)
	}()
	if err == nil {
		// the fetch belongs to all who wait for it, not to the one who asked first
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		if err = h.b.Fetch(ctx, group, attID, sha, dir); err == nil {
			var ok bool
			if c.path, ok = oneFile(dir); !ok {
				err = errors.New("the fetch gave no file")
			}
		}
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}
	c.err = err
	return c.path, c.err
}

func oneFile(dir string) (string, bool) {
	es, err := os.ReadDir(dir)
	if err != nil || len(es) != 1 || !es[0].Type().IsRegular() || strings.HasSuffix(es[0].Name(), ".tmp") {
		return "", false
	}
	return filepath.Join(dir, es[0].Name()), true
}

// prune takes what is too old, and then the oldest, out of the cache until it is under its budget.
func prune(cache string, busy map[string]bool) {
	es, err := os.ReadDir(cache)
	if err != nil {
		return
	}
	type entry struct {
		path string
		mod  time.Time
		size int64
	}
	var all []entry
	var total int64
	for _, e := range es {
		if busy[e.Name()] { // being fetched: it looks old and empty
			continue
		}
		dir := filepath.Join(cache, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) > cacheLife {
			_ = os.RemoveAll(dir)
			continue
		}
		var size int64
		if p, ok := oneFile(dir); ok {
			if st, err := os.Stat(p); err == nil {
				size = st.Size()
			}
		}
		all = append(all, entry{dir, fi.ModTime(), size})
		total += size
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	for _, e := range all {
		if total <= cacheBudget-maxFile { // room for the next one
			break
		}
		_ = os.RemoveAll(e.path)
		total -= e.size
	}
}

// drop is POST /api/chat/drop {group, id}: a file that waits and will not be sent (the person took it back).
func (h *handlers) drop(w http.ResponseWriter, r *http.Request) {
	var req struct{ Group, ID string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		web.Fail(w, http.StatusBadRequest, "bad request")
		return
	}
	h.files.mu.Lock()
	f := h.files.files[req.ID]
	mine := f != nil && f.group == req.Group
	h.files.mu.Unlock()
	if mine {
		h.files.drop(req.ID)
	}
	web.Reply(w, map[string]bool{"ok": true})
}
