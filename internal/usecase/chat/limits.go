package chat

import "time"

// The limits of the chat (docs/plan/ui-chat.md §3.1 and §3.2).
const (
	MaxLinesPerSecond       = 200 // all senders together
	MaxLinesPerSenderSecond = 50
	MaxConversations        = 500 // per group
	MaxNewTopicsPerHour     = 100 // per sender
)

// Limiter takes at most so many lines in each second: in all, and from one sender. A line that is over the limit is counted and
// dropped; the page says so. It does not block.
type Limiter struct {
	second  int64
	total   int
	senders map[string]int
	Dropped int // counted since the start
}

// Allow says whether the line of the sender at that time is taken.
func (l *Limiter) Allow(sender string, now time.Time) bool {
	if s := now.Unix(); s != l.second || l.senders == nil {
		l.second, l.total, l.senders = s, 0, map[string]int{}
	}
	if l.total >= MaxLinesPerSecond || l.senders[sender] >= MaxLinesPerSenderSecond {
		l.Dropped++
		return false
	}
	l.total++
	l.senders[sender]++
	return true
}

// Admission decides whether a message may make a conversation that does not exist yet: a machine can publish on any topic, and the
// pairs and machines are only those of the group (Classify), so the topics are what a flood can fill. Over the limit the message
// goes to "alle" only; nothing that exists is ever deleted for it.
type Admission struct {
	hour   map[string][]time.Time
	Denied int
}

// Topic says whether the sender may make a new topic now, given how many conversations there are.
func (a *Admission) Topic(sender string, conversations int, now time.Time) bool {
	if a.hour == nil {
		a.hour = map[string][]time.Time{}
	}
	recent := a.hour[sender][:0]
	for _, t := range a.hour[sender] {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	a.hour[sender] = recent
	if conversations >= MaxConversations || len(recent) >= MaxNewTopicsPerHour {
		a.Denied++
		return false
	}
	a.hour[sender] = append(recent, now)
	return true
}
