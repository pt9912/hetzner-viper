package natsmcp

import (
	"sync"
	"time"
)

// Entry is a message in the inbox.
type Entry struct {
	Handle  int // assigned by the receiver; tools refer to an entry by it, never by the sender's ID
	From    string
	Kind    string // "msg" or "req"
	ID      string // the sender's ID of a request (needed for the answer)
	Topic   string
	Text    string
	TS      time.Time
	Replied bool
}

// Inbox keeps the last messages. No sender can push the others out: each may hold only a part of
// the space.
type Inbox struct {
	mu        sync.Mutex
	max, per  int
	next      int
	entries   []*Entry
	droppedBy map[string]int
}

// NewInbox returns an inbox for at most max messages, at most per of them from one sender.
func NewInbox(limit, per int) *Inbox {
	return &Inbox{max: limit, per: per, next: 1, droppedBy: map[string]int{}}
}

// Add stores a message and returns it with its handle.
func (b *Inbox) Add(e Entry) Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	e.Handle = b.next
	b.next++
	b.entries = append(b.entries, &e)
	// the sender's own oldest message goes first when it has more than its share
	for count := b.count(e.From); count > b.per; count-- {
		b.dropOldest(func(x *Entry) bool { return x.From == e.From })
	}
	for len(b.entries) > b.max {
		b.dropOldest(func(*Entry) bool { return true })
	}
	return e
}

func (b *Inbox) count(from string) int {
	n := 0
	for _, e := range b.entries {
		if e.From == from {
			n++
		}
	}
	return n
}

func (b *Inbox) dropOldest(match func(*Entry) bool) {
	for i, e := range b.entries {
		if match(e) {
			b.droppedBy[e.From]++
			b.entries = append(b.entries[:i], b.entries[i+1:]...)
			return
		}
	}
}

// Since returns the messages with a handle greater than handle, oldest first.
func (b *Inbox) Since(handle int) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Entry
	for _, e := range b.entries {
		if e.Handle > handle {
			out = append(out, *e)
		}
	}
	return out
}

// Get returns the message with that handle.
func (b *Inbox) Get(handle int) (Entry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.entries {
		if e.Handle == handle {
			return *e, true
		}
	}
	return Entry{}, false
}

// MarkReplied marks a request as answered; false if it was answered already or is gone.
func (b *Inbox) MarkReplied(handle int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.entries {
		if e.Handle == handle {
			if e.Replied {
				return false
			}
			e.Replied = true
			return true
		}
	}
	return false
}

// Dropped is how many messages of that sender were pushed out.
func (b *Inbox) Dropped(from string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.droppedBy[from]
}
