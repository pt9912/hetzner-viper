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
	"sync"
	"time"
	"unicode/utf8"
)

// Envelope is the content of every message the servers exchange.
type Envelope struct {
	V     int    `json:"v"`
	Kind  string `json:"kind"`            // msg, req, res, presence
	ID    string `json:"id,omitempty"`    // chosen by the sender of a request
	Re    string `json:"re,omitempty"`    // the ID of the request an answer belongs to
	Text  string `json:"text,omitempty"`  // the message
	State string `json:"state,omitempty"` // presence: up or bye
	Query bool   `json:"query,omitempty"` // presence: please answer with yours
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
		default:
			return fmt.Errorf("unknown field %q", truncate(key, 32))
		}
		if seen[key] {
			return fmt.Errorf("field %q twice", key)
		}
		seen[key] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return err
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

func checkEnvelope(e Envelope, maxText int) error {
	switch e.Kind {
	case "msg", "req", "res", "presence":
	default:
		return fmt.Errorf("kind %q", truncate(e.Kind, 32))
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
	return nil
}

// Peers is who said recently that it is there.
type Peers struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

// NewPeers returns a table in which a machine counts as present for ttl after its last message.
func NewPeers(ttl time.Duration) *Peers { return &Peers{seen: map[string]time.Time{}, ttl: ttl} }

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
	p.mu.Unlock()
}

// Peer is a machine that is present.
type Peer struct {
	Name string
	Age  time.Duration
}

// Present returns the machines seen within the time to live, by name.
func (p *Peers) Present(now time.Time) []Peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Peer
	for n, t := range p.seen {
		if age := now.Sub(t); age <= p.ttl {
			out = append(out, Peer{Name: n, Age: age})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// truncate cuts s to n bytes (at a character) with "..." when it is longer: text of another machine in a log line.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}
