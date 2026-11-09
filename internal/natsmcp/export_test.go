package natsmcp

import "time"

// Test-only exports of the unexported identifiers the external tests use.

// MaxTopics exports maxTopics.
const MaxTopics = maxTopics

// SetJitter sets how long a service waits before it answers a presence query.
func (s *Service) SetJitter(f func() time.Duration) { s.jitter = f }

// Inbox exports the inbox of the service.
func (s *Service) Inbox() *Inbox { return s.inbox }

// PeersTable exports the table of present machines.
func (s *Service) PeersTable() *Peers { return s.peers }

// Cfg exports the configuration.
func (s *Service) Cfg() Config { return s.cfg }

// RawPublish publishes bytes on any subject through the connection of the service: what a
// machine that does not follow the rules would do. The NATS server decides whether it is allowed.
func (s *Service) RawPublish(subject string, data []byte) error { return s.nc.Publish(subject, data) }

// FlushRaw makes sure that the server has received everything published so far.
func (s *Service) FlushRaw() error { return s.nc.Flush() }

// PublishEnvelope publishes a message of ours on any subject.
func (s *Service) PublishEnvelope(subject string, e Envelope) error { return s.publish(subject, e) }

// WaiterIDs are the IDs of the requests that wait for an answer.
func (s *Service) WaiterIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id := range s.waiters {
		ids = append(ids, id)
	}
	return ids
}
