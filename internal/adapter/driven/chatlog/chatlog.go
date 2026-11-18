// Package chatlog keeps the history of the chat (port chatlog.Store) in files, one per conversation, or in memory: JSON lines under
// <state>/chat/<group>/, rights 0600, a folder 0700. Each conversation has two files (the current one and the one before it), so
// that the oldest lines fall away in blocks and nothing is rewritten for each line.
package chatlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pt9912/hetzner-viper/internal/model"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
)

// The limits (docs/plan/ui-chat.md §3.1).
const (
	RotateLines     = 5000
	RotateBytes     = 10 << 20
	MaxGroupBytes   = 1 << 30
	maxLine         = 32 << 10
	counterEvery    = 50
	DefaultKeepDays = 90
)

var keyRe = regexp.MustCompile(`^(alle|(rechner|thema)__[a-z0-9-]+|paar__[a-z0-9-]+__[a-z0-9-]+)$`)

// storage is where the bytes are: a folder, or memory.
type storage interface {
	read(name string) ([]byte, error)
	append(name string, line []byte) error
	write(name string, data []byte) error
	rename(from, to string) error
	remove(name string) error
	list(dir string) ([]string, error)
	stat(name string) (size int64, mod time.Time, err error)
}

// Store is the history.
type Store struct {
	st       storage
	keep     time.Duration
	mu       sync.Mutex
	groups   map[string]*groupState
	rotation struct{ lines, bytes int64 }
}

type groupState struct {
	n       int64
	appends int
	tidy    map[string]bool  // the file ends with a line end (looked at once)
	lines   map[string]int64 // the lines of the current file of each conversation
	conv    map[string]*port.Conversation
	read    map[string]int64
	loaded  bool
}

// Open makes a store in the folder (created with 0700); keepDays is how long records are kept.
func Open(dir string, keepDays int) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return newStore(dirStorage{dir}, keepDays), nil
}

// Memory makes a store that keeps nothing on the disk (viper ui-chat --no-history).
func Memory(keepDays int) *Store { return newStore(&memStorage{files: map[string][]byte{}}, keepDays) }

func newStore(st storage, keepDays int) *Store {
	if keepDays <= 0 {
		keepDays = DefaultKeepDays
	}
	s := &Store{st: st, keep: time.Duration(keepDays) * 24 * time.Hour, groups: map[string]*groupState{}}
	s.rotation.lines, s.rotation.bytes = RotateLines, RotateBytes
	return s
}

func checkNames(group, key string) error {
	if err := model.CheckNATSName("group", group); err != nil {
		return err
	}
	if key != "" && !keyRe.MatchString(key) {
		return fmt.Errorf("%q is no conversation", key)
	}
	return nil
}

func file(group, key string) string   { return group + "/" + key + ".jsonl" }
func older(group, key string) string  { return group + "/" + key + ".1.jsonl" }
func counterFile(group string) string { return group + "/.n" }
func readFile(group string) string    { return group + "/read.json" }

// state loads what a group has on first use: the number to go on from and the markers of what was read.
func (s *Store) state(group string) *groupState {
	g := s.groups[group]
	if g != nil && g.loaded {
		return g
	}
	g = &groupState{tidy: map[string]bool{}, lines: map[string]int64{}, conv: map[string]*port.Conversation{}, read: map[string]int64{}}
	if b, err := s.st.read(counterFile(group)); err == nil {
		_, _ = fmt.Sscan(strings.TrimSpace(string(b)), &g.n)
	}
	if b, err := s.st.read(readFile(group)); err == nil {
		_ = json.Unmarshal(b, &g.read)
	}
	if g.read == nil {
		g.read = map[string]int64{}
	}
	names, _ := s.st.list(group)
	for _, name := range names {
		if strings.HasSuffix(name, ".1.jsonl") { // the number does not go back when only the older file has the newest
			recs, _ := s.parse(group + "/" + name)
			for _, r := range recs {
				g.n = max(g.n, r.N)
			}
			continue
		}
		key, ok := currentKey(name)
		if !ok {
			continue
		}
		recs, lines := s.parse(file(group, key))
		g.lines[key] = lines
		for _, r := range recs {
			g.n = max(g.n, r.N)
		}
		s.summarize(g, key, group)
	}
	g.loaded = true
	s.groups[group] = g
	return g
}

func currentKey(name string) (string, bool) {
	if !strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".1.jsonl") {
		return "", false
	}
	key := strings.TrimSuffix(name, ".jsonl")
	return key, keyRe.MatchString(key)
}

// parse reads the records of a file (a line that is no record is skipped) and how many lines it has.
func (s *Store) parse(name string) ([]port.Record, int64) {
	b, err := s.st.read(name)
	if err != nil {
		return nil, 0
	}
	var out []port.Record
	var lines int64
	for _, line := range bytes.Split(b, []byte{'\n'}) { // no limit of a line: one long line must not hide the ones after it
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		lines++
		var r port.Record
		if json.Unmarshal(line, &r) == nil && r.N > 0 {
			out = append(out, r)
		}
	}
	return out, lines
}

// records are the records of a conversation, oldest first (the older file, then the current one), without those that are too old.
func (s *Store) records(group, key string, now time.Time) []port.Record {
	old, _ := s.parse(older(group, key))
	cur, _ := s.parse(file(group, key))
	all := append(old, cur...)
	cut := now.Add(-s.keep)
	out := all[:0]
	for _, r := range all {
		if !r.T.Before(cut) {
			out = append(out, r)
		}
	}
	return out
}

// summarize makes the entry of the list from the records.
func (s *Store) summarize(g *groupState, key, group string) {
	recs := s.records(group, key, time.Now())
	if len(recs) == 0 {
		delete(g.conv, key)
		return
	}
	c := &port.Conversation{Key: key, Last: recs[len(recs)-1]}
	for _, r := range recs {
		if r.N > g.read[key] && r.Dir == "in" {
			c.Unread++
		}
	}
	g.conv[key] = c
}

// Append gives the record the next N of the group and writes it into each of the conversations.
func (s *Store) Append(group string, keys []string, r port.Record) (port.Record, error) {
	for _, k := range keys {
		if err := checkNames(group, k); err != nil {
			return r, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	g.n++
	r.N = g.n
	if r.T.IsZero() {
		r.T = time.Now().UTC()
	}
	r.T = r.T.UTC()
	r = clipped(r)
	line, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	for _, k := range keys {
		if err := s.appendTo(g, group, k, line); err != nil {
			return r, err
		}
		c := g.conv[k]
		if c == nil {
			c = &port.Conversation{Key: k}
			g.conv[k] = c
		}
		c.Last = r
		if r.Dir == "in" {
			c.Unread++
		}
	}
	if g.appends++; g.appends%counterEvery == 0 {
		_ = s.st.write(counterFile(group), []byte(fmt.Sprintf("%d\n", g.n)))
	}
	return r, nil
}

// clipped cuts every text of a record to a size: the sender of a line chooses all of them, and a record is one line of a file.
func clipped(r port.Record) port.Record {
	r.Text = clip(r.Text, maxLine)
	r.From, r.To = clip(r.From, 128), clip(r.To, 128)
	r.Topic, r.ID, r.Re = clip(r.Topic, 256), clip(r.ID, 256), clip(r.Re, 256)
	r.AttID, r.AttType, r.AttName, r.AttSHA = clip(r.AttID, 256), clip(r.AttType, 256), clip(r.AttName, 256), clip(r.AttSHA, 256)
	return r
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (s *Store) appendTo(g *groupState, group, key string, line []byte) error {
	name := file(group, key)
	size, _, _ := s.st.stat(name)
	if g.lines[key] >= s.rotation.lines || size >= s.rotation.bytes {
		_ = s.st.remove(older(group, key))
		if err := s.st.rename(name, older(group, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		g.lines[key] = 0
		g.tidy[key] = true
	}
	if !g.tidy[key] { // a file that a crash left without the end of its last line: the next record must not grow into it
		if b, err := s.st.read(name); err == nil && len(b) > 0 && b[len(b)-1] != '\n' {
			if err := s.st.append(name, []byte{'\n'}); err != nil {
				return err
			}
		}
		g.tidy[key] = true
	}
	g.lines[key]++
	return s.st.append(name, append(line, '\n'))
}

// Tail is the last limit records of a conversation, oldest first.
func (s *Store) Tail(group, key string, limit int) ([]port.Record, error) {
	if err := checkNames(group, key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := s.records(group, key, time.Now())
	if limit > 0 && len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	return append([]port.Record(nil), recs...), nil
}

// After is the records with a larger N, the oldest of them, at most limit.
func (s *Store) After(group, key string, n int64, limit int) ([]port.Record, error) {
	if err := checkNames(group, key); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []port.Record
	for _, r := range s.records(group, key, time.Now()) {
		if r.N > n {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// Conversations lists the conversations of a group, the one with the newest message first.
func (s *Store) Conversations(group string) ([]port.Conversation, error) {
	if err := checkNames(group, ""); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	out := []port.Conversation{}
	for _, c := range g.conv {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.N > out[j].Last.N })
	return out, nil
}

// MarkRead says that the person has seen everything up to n.
func (s *Store) MarkRead(group, key string, n int64) error {
	if err := checkNames(group, key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	if n <= g.read[key] {
		return nil
	}
	g.read[key] = n
	if c := g.conv[key]; c != nil {
		c.Unread = 0
		for _, r := range s.records(group, key, time.Now()) {
			if r.N > n && r.Dir == "in" {
				c.Unread++
			}
		}
	}
	b, _ := json.Marshal(g.read)
	return s.st.write(readFile(group), b)
}

func (s *Store) Read(group, key string) (int64, error) {
	if err := checkNames(group, key); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	return g.read[key], nil
}

// Delete removes a conversation (key "": all of the group).
func (s *Store) Delete(group, key string) error {
	if err := checkNames(group, key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	keys := []string{key}
	if key == "" {
		keys = keys[:0]
		for k := range g.conv {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		_ = s.st.remove(file(group, k))
		_ = s.st.remove(older(group, k))
		delete(g.conv, k)
		delete(g.lines, k)
		delete(g.read, k)
	}
	b, _ := json.Marshal(g.read)
	_ = s.st.write(counterFile(group), []byte(fmt.Sprintf("%d\n", g.n))) // what was numbered stays numbered
	return s.st.write(readFile(group), b)
}

// Search finds records whose text has the word (any case), the newest first, at most limit.
func (s *Store) Search(group, word string, limit int) ([]port.Hit, error) {
	if err := checkNames(group, ""); err != nil {
		return nil, err
	}
	word = strings.ToLower(word)
	if word == "" {
		return nil, errors.New("search for a word")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	var hits []port.Hit
	seen := map[int64]bool{} // a record is in its conversation and in "alle": once
	keys := make([]string, 0, len(g.conv))
	for key := range g.conv {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return (keys[i] == "alle") != (keys[j] == "alle") && keys[j] == "alle" })
	for _, key := range keys {
		for _, r := range s.records(group, key, time.Now()) {
			if !seen[r.N] && strings.Contains(strings.ToLower(r.Text), word) {
				seen[r.N] = true
				hits = append(hits, port.Hit{Key: key, Record: r})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Record.N > hits[j].Record.N })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Prune removes files whose newest line is older than the time, and, when the group is over its size, the older files first.
func (s *Store) Prune(group string, now time.Time) error {
	if err := checkNames(group, ""); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.state(group)
	names, _ := s.st.list(group)
	type f struct {
		name string
		size int64
		mod  time.Time
	}
	var files []f
	var total int64
	for _, n := range names {
		if !strings.HasSuffix(n, ".jsonl") {
			continue
		}
		size, mod, err := s.st.stat(group + "/" + n)
		if err != nil {
			continue
		}
		if now.Sub(mod) > s.keep {
			_ = s.st.remove(group + "/" + n)
			delete(g.lines, strings.TrimSuffix(strings.TrimSuffix(n, ".jsonl"), ".1"))
			continue
		}
		files = append(files, f{n, size, mod})
		total += size
	}
	sort.Slice(files, func(i, j int) bool { // the files of an earlier block first, then the oldest
		a, b := strings.HasSuffix(files[i].name, ".1.jsonl"), strings.HasSuffix(files[j].name, ".1.jsonl")
		if a != b {
			return a
		}
		return files[i].mod.Before(files[j].mod)
	})
	for _, x := range files {
		if total <= MaxGroupBytes {
			break
		}
		_ = s.st.remove(group + "/" + x.name)
		delete(g.lines, strings.TrimSuffix(strings.TrimSuffix(x.name, ".jsonl"), ".1"))
		total -= x.size
	}
	_ = s.st.write(counterFile(group), []byte(fmt.Sprintf("%d\n", g.n)))
	for key := range g.conv { // the list follows the files
		s.summarize(g, key, group)
	}
	return nil
}

// Close writes the number to go on from.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for group, g := range s.groups {
		if err := s.st.write(counterFile(group), []byte(fmt.Sprintf("%d\n", g.n))); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// dirStorage keeps the files in a folder.
type dirStorage struct{ dir string }

func (d dirStorage) path(name string) string { return filepath.Join(d.dir, filepath.FromSlash(name)) }

func (d dirStorage) read(name string) ([]byte, error) { return os.ReadFile(d.path(name)) }

func (d dirStorage) append(name string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(d.path(name)), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(d.path(name), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	if cerr := f.Close(); werr == nil { // closed before any rename: Windows cannot rename an open file
		werr = cerr
	}
	return werr
}

func (d dirStorage) write(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(d.path(name)), 0o700); err != nil {
		return err
	}
	tmp := d.path(name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path(name)) // a crash leaves the old file or the new one, not half of one
}

func (d dirStorage) rename(from, to string) error { return os.Rename(d.path(from), d.path(to)) }

func (d dirStorage) remove(name string) error {
	if err := os.Remove(d.path(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d dirStorage) list(dir string) ([]string, error) {
	es, err := os.ReadDir(d.path(dir))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (d dirStorage) stat(name string) (int64, time.Time, error) {
	fi, err := os.Stat(d.path(name))
	if err != nil {
		return 0, time.Time{}, err
	}
	return fi.Size(), fi.ModTime(), nil
}

// memStorage keeps the files in memory.
type memStorage struct {
	mu    sync.Mutex
	files map[string][]byte
	mod   map[string]time.Time
}

func (m *memStorage) read(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (m *memStorage) append(name string, line []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = append(m.files[name], line...)
	m.touch(name)
	return nil
}

func (m *memStorage) write(name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = append([]byte(nil), data...)
	m.touch(name)
	return nil
}

func (m *memStorage) touch(name string) {
	if m.mod == nil {
		m.mod = map[string]time.Time{}
	}
	m.mod[name] = time.Now()
}

func (m *memStorage) rename(from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[from]
	if !ok {
		return os.ErrNotExist
	}
	m.files[to] = b
	delete(m.files, from)
	m.touch(to)
	return nil
}

func (m *memStorage) remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, name)
	return nil
}

func (m *memStorage) list(dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for n := range m.files {
		if rest, ok := strings.CutPrefix(n, dir+"/"); ok && !strings.Contains(rest, "/") {
			out = append(out, rest)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *memStorage) stat(name string) (int64, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[name]
	if !ok {
		return 0, time.Time{}, os.ErrNotExist
	}
	return int64(len(b)), m.mod[name], nil
}
