package natsmcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Envelope is the content of every message the servers exchange.
type Envelope struct {
	V     int    `json:"v"`
	Kind  string `json:"kind"`            // msg, req, res, presence, topics, caps
	ID    string `json:"id,omitempty"`    // chosen by the sender of a request
	Re    string `json:"re,omitempty"`    // the ID of the request an answer belongs to
	Text  string `json:"text,omitempty"`  // the message; for topics the names of the topics, separated by blanks; for caps the version, then the capabilities
	State string `json:"state,omitempty"` // presence: up or bye
	Query bool   `json:"query,omitempty"` // presence: please answer with yours
	// Att is a file in the object store of the group (docs/plan/nats-objekte.md): msg and req only. A version that does not know the
	// field refuses the whole message.
	Att *Attachment `json:"att,omitempty"`
}

var idRe = regexp.MustCompile(`^[a-f0-9]{16,64}$`)

// maxWire is the largest message the server looks at.
const maxWire = 32 << 10

// newID is a random request ID.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no randomness
	}
	return hex.EncodeToString(b)
}

// Decode reads and checks a message. Anything that does not match is refused: wrong version or kind,
// an ID that is not hex, a text longer than maxText. Bytes that are not UTF-8 are replaced while the
// JSON is read, so the text is always valid.
func Decode(data []byte, maxText int) (Envelope, error) {
	if len(data) > maxWire {
		return Envelope{}, fmt.Errorf("message of %d bytes", len(data))
	}
	if err := checkKeys(data); err != nil {
		return Envelope{}, err
	}
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Envelope{}, err
	}
	if e.V != 1 {
		return Envelope{}, fmt.Errorf("version %d", e.V)
	}
	if err := checkEnvelope(e, maxText); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

// checkKeys makes the reading of a message strict: the top level is an object, every key is one of the
// fields of the envelope in exactly that spelling (encoding/json would also take "Kind" or "KIND"), and no key
// is there twice (encoding/json takes the last, other programs the first: a way to show two readers two
// different messages). A protocol change raises "v", so a field this version does not know is a refusal.
func checkKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := t.(string)
		switch key {
		case "v", "kind", "id", "re", "text", "state", "query":
		case "att":
		default:
			return fmt.Errorf("unknown field %q", truncate(key))
		}
		if seen[key] {
			return fmt.Errorf("field %q twice", key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		if key == "att" {
			if err := checkAttKeys(raw); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("text after the JSON object")
	}
	return nil
}

// checkAttKeys is checkKeys for the reference to an attachment: the five fields, each once, in that spelling.
func checkAttKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return errors.New("att is not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := t.(string)
		switch key {
		case "id", "type", "name", "size", "sha256":
		default:
			return fmt.Errorf("unknown field %q in att", truncate(key))
		}
		if seen[key] {
			return fmt.Errorf("field %q twice in att", key)
		}
		seen[key] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
		}
	}
	return nil
}

var (
	attIDRe  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	sha256Re = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// checkAttachment says what is wrong with the reference to an attachment.
func checkAttachment(a *Attachment) error {
	switch {
	case !attIDRe.MatchString(a.ID):
		return errors.New("attachment: bad id")
	case !validType(a.Type):
		return fmt.Errorf("attachment: type %q", truncate(a.Type))
	case !validAttachName(a.Name):
		return errors.New("attachment: bad name")
	case a.Size < 1 || a.Size > maxAttachment:
		return fmt.Errorf("attachment: size %d", a.Size)
	case !sha256Re.MatchString(a.SHA256):
		return errors.New("attachment: bad checksum")
	}
	return nil
}

func checkEnvelope(e Envelope, maxText int) error {
	switch e.Kind {
	case "msg", "req", "res", "presence", "topics", "caps":
	default:
		return fmt.Errorf("kind %q", truncate(e.Kind))
	}
	if e.ID != "" && !idRe.MatchString(e.ID) || e.Kind == "req" && e.ID == "" {
		return errors.New("bad id")
	}
	if e.Kind == "res" && !idRe.MatchString(e.Re) {
		return errors.New("bad re")
	}
	if e.Kind == "presence" && e.State != "up" && e.State != "bye" {
		return fmt.Errorf("presence state %q", e.State)
	}
	if len(e.Text) > maxText || !utf8.ValidString(e.Text) {
		return fmt.Errorf("text of %d bytes", len(e.Text))
	}
	return checkAtt(e)
}

// checkAtt checks the attachment of a message, if it has one: msg and req only.
func checkAtt(e Envelope) error {
	if e.Att == nil {
		return nil
	}
	if e.Kind != "msg" && e.Kind != "req" {
		return fmt.Errorf("an attachment in a message of the kind %q", e.Kind)
	}
	return checkAttachment(e.Att)
}

// Peers is who said recently that it is there, and which topics they said they subscribed to.
type Peers struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	topics map[string]topicList
	caps   map[string]capsInfo
	ttl    time.Duration
}

// capsInfo is what a machine said about its version and capabilities, and when.
type capsInfo struct {
	version string
	caps    []string
	at      time.Time
}

type topicList struct {
	names []string
	at    time.Time
}

// NewPeers returns a table in which a machine counts as present for ttl after its last message.
func NewPeers(ttl time.Duration) *Peers {
	return &Peers{seen: map[string]time.Time{}, topics: map[string]topicList{}, caps: map[string]capsInfo{}, ttl: ttl}
}

// SetTopics notes the topics a machine said it subscribed to.
func (p *Peers) SetTopics(name string, names []string, at time.Time) {
	p.mu.Lock()
	p.topics[name] = topicList{names: names, at: at}
	p.mu.Unlock()
}

// SetCaps notes the version and the capabilities a machine said it has.
func (p *Peers) SetCaps(name, version string, caps []string, at time.Time) {
	p.mu.Lock()
	p.caps[name] = capsInfo{version: version, caps: caps, at: at}
	p.mu.Unlock()
}

var (
	versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$`)
	capRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)
)

const maxCaps = 8

// ParseCaps reads the text of a caps announcement: a version, then the capabilities, separated by blanks. The version is
// short and made of the characters of a version number, a capability is a short lower-case word, at most maxCaps of them,
// none twice. The result is sorted.
func ParseCaps(text string) (version string, caps []string, err error) {
	f := strings.Fields(text)
	if len(f) == 0 || !versionRe.MatchString(f[0]) {
		return "", nil, errors.New("no version")
	}
	seen := map[string]bool{}
	for _, c := range f[1:] {
		if !capRe.MatchString(c) {
			return "", nil, fmt.Errorf("%q is not a capability", truncate(c))
		}
		if !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}
	if len(caps) > maxCaps {
		return "", nil, fmt.Errorf("%d capabilities", len(caps))
	}
	sort.Strings(caps)
	return f[0], caps, nil
}

// ParseTopics reads the text of a topics announcement: the names, separated by blanks, each a valid name, at most limit of
// them, none twice. The result is sorted.
func ParseTopics(text string, limit int) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Fields(text) {
		if !ValidName(t) {
			return nil, fmt.Errorf("%q is not a topic name", truncate(t))
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > limit {
		return nil, fmt.Errorf("%d topics", len(out))
	}
	sort.Strings(out)
	return out, nil
}

// Seen notes that a machine is there.
func (p *Peers) Seen(name string, at time.Time) {
	p.mu.Lock()
	p.seen[name] = at
	p.mu.Unlock()
}

// Gone removes a machine that said goodbye.
func (p *Peers) Gone(name string) {
	p.mu.Lock()
	delete(p.seen, name)
	delete(p.topics, name)
	delete(p.caps, name)
	p.mu.Unlock()
}

// Peer is a machine that is present. TopicsKnown is false for a machine that has not said which topics it subscribed to
// (an older version does not, or its announcement has not come yet or is older than the time to live).
type Peer struct {
	Name        string
	Age         time.Duration
	Topics      []string
	TopicsKnown bool
	// Version and Caps are what the machine said about its viper-nats-mcp; CapsKnown is false for an older version (it says
	// nothing), or while its announcement has not come yet or is older than the time to live.
	Version   string
	Caps      []string
	CapsKnown bool
}

// Present returns the machines seen within the time to live, by name.
func (p *Peers) Present(now time.Time) []Peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Peer
	for n, t := range p.seen {
		if age := now.Sub(t); age <= p.ttl {
			peer := Peer{Name: n, Age: age}
			if tl, ok := p.topics[n]; ok && now.Sub(tl.at) <= p.ttl {
				peer.Topics, peer.TopicsKnown = tl.names, true
			}
			if ci, ok := p.caps[n]; ok && now.Sub(ci.at) <= p.ttl {
				peer.Version, peer.Caps, peer.CapsKnown = ci.version, ci.caps, true
			}
			out = append(out, peer)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// truncate cuts s to 32 bytes (at a character) with "..." when it is longer: text of another machine in a log line.
func truncate(s string) string {
	n := 32
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
