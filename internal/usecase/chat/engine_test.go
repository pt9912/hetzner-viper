package chat_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/chatlog"
	"github.com/pt9912/hetzner-viper/internal/usecase/chat"
)

// fakeConn is a connection that a test drives: what is sent on out becomes the stream, stderr lines go to the error output; Watch
// ends when the context ends (like watch, which does not notice a dead tunnel).
type fakeConn struct {
	out, errs chan string
	dead      chan struct{}
	closed    atomic.Bool
}

func newConn() *fakeConn {
	return &fakeConn{out: make(chan string, 1000), errs: make(chan string, 10), dead: make(chan struct{})}
}

func (c *fakeConn) Watch(ctx context.Context, stdout, stderr io.Writer) error {
	for {
		select {
		case s := <-c.out:
			_, _ = io.WriteString(stdout, s+"\n")
		case s := <-c.errs:
			_, _ = io.WriteString(stderr, s+"\n")
		case <-ctx.Done():
			return nil
		}
	}
}
func (c *fakeConn) Dead() <-chan struct{} { return c.dead }
func (c *fakeConn) Close()                { c.closed.Store(true) }

type fakeConnector struct {
	mu    sync.Mutex
	conns []*fakeConn
	errs  []error
	calls int
}

func (f *fakeConnector) Connect(ctx context.Context, group string) (chat.Conn, error) { //nolint:ireturn // the contract of the connector
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.errs) > 0 {
		e := f.errs[0]
		f.errs = f.errs[1:]
		return nil, e
	}
	c := newConn()
	f.conns = append(f.conns, c)
	return c, nil
}

func (f *fakeConnector) conn(i int) *fakeConn {
	for k := 0; k < 300; k++ {
		f.mu.Lock()
		if len(f.conns) > i {
			c := f.conns[i]
			f.mu.Unlock()
			return c
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func grp() chat.Group {
	return chat.Group{Operator: "operator", Machines: map[string]bool{"w1": true, "w2": true, "w3": true}}
}

func start(t *testing.T, cn chat.Connector, redact func(string) string) (*chat.Engine, *chatlog.Store, *chat.Subscription) {
	t.Helper()
	st := chatlog.Memory(0)
	e := chat.New(chat.Options{Store: st, Connector: cn, Redact: redact, Wait: 20 * time.Millisecond})
	sub := e.Subscribe()
	t.Cleanup(e.Stop)
	e.Ensure("dev", grp())
	return e, st, sub
}

func next(t *testing.T, sub *chat.Subscription, kind string) chat.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %s event", kind)
		}
	}
}

func waitStatus(t *testing.T, sub *chat.Subscription, status string) chat.Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == "status" && ev.Status.Status == status {
				return ev
			}
		case <-deadline:
			t.Fatalf("the status never was %s", status)
		}
	}
}

func TestTheStreamBecomesConversationsPresenceAndAllWithTheTextCleanedAfterTheJSONWasRead(t *testing.T) {
	cn := &fakeConnector{}
	secret := `p"a&s<s\word`
	e, st, sub := start(t, cn, func(s string) string { return strings.ReplaceAll(s, secret, "[hidden]") })
	c := cn.conn(0)
	c.out <- `{"event":"ready","me":"operator","group":"dev"}`
	waitStatus(t, sub, chat.StatusConnected)
	c.out <- `{"event":"message","from":"w1","via":"presence","type":"presence","state":"up"}`
	c.out <- `{"event":"message","from":"w1","via":"topics","type":"topics","text":"build"}`
	c.out <- `{"event":"message","from":"w2","to":"operator","via":"out","type":"msg","text":"the secret is p\"a&s<s\\word, really"}`
	ev := next(t, sub, "message")
	if strings.Join(ev.Keys, ",") != "rechner__w2,alle" || ev.Record.N != 1 || ev.Record.Text != "the secret is [hidden], really" || ev.Record.Dir != "in" {
		t.Fatalf("%+v %+v", ev, ev.Record)
	}
	c.out <- `{"event":"message","from":"w1","to":"w3","via":"out","type":"req","id":"` + strings.Repeat("a", 16) + `","text":"hi"}`
	c.out <- `{"event":"message","from":"w1","via":"pub","topic":"build","type":"msg","text":"green"}`
	next(t, sub, "message")
	next(t, sub, "message")
	for _, k := range []string{"alle", "paar__w1__w3", "thema__build", "rechner__w2"} {
		if got, _ := st.Tail("dev", k, 10); len(got) == 0 {
			t.Errorf("nothing in %s", k)
		}
	}
	s := e.Snapshot("dev", []string{"build", "alerts"})
	if s.Machines["w1"].State != "here" || s.Machines["w3"].State != "unknown" || len(s.Topics["build"].Subscribers) != 1 || s.Topics["build"].Known {
		t.Errorf("%+v", s)
	}
	if len(s.Topics["alerts"].Subscribers) != 0 || s.Topics["alerts"].Known {
		t.Errorf("a topic of which not every machine has reported is not yet 'nobody': %+v", s.Topics["alerts"])
	}
}

func TestADeadTunnelEndsTheReadingAndANewConnectionIsMade(t *testing.T) {
	cn := &fakeConnector{}
	_, _, sub := start(t, cn, nil)
	c := cn.conn(0)
	c.out <- `{"event":"ready"}`
	waitStatus(t, sub, chat.StatusConnected)
	close(c.dead) // the ssh process of the tunnel is gone: watch does not know it
	ev := waitStatus(t, sub, chat.StatusDisturbed)
	if !strings.Contains(ev.Status.Reason, "tunnel") {
		t.Errorf("the reason: %q", ev.Status.Reason)
	}
	c2 := cn.conn(1)
	if c2 == nil || !c.closed.Load() {
		t.Fatal("no new connection, or the old one is not closed")
	}
	c2.out <- `{"event":"ready"}`
	waitStatus(t, sub, chat.StatusConnected)
}

func TestAnUnknownKeyIsNotTriedAgainUntilTheLoginIsRepeated(t *testing.T) {
	cn := &fakeConnector{}
	e, _, sub := start(t, cn, nil)
	c := cn.conn(0)
	c.out <- `{"event":"ready"}`
	waitStatus(t, sub, chat.StatusConnected)
	c.errs <- "2026-11-18T12:00:00Z server error: nats: authorization violation (1 so far)"
	waitStatus(t, sub, chat.StatusAuth)
	time.Sleep(200 * time.Millisecond)
	cn.mu.Lock()
	calls := cn.calls
	cn.mu.Unlock()
	if calls != 1 {
		t.Fatalf("it tried again by itself: %d connections", calls)
	}
	e.Retry("dev")
	if cn.conn(1) == nil {
		t.Fatal("no new try after the login")
	}
}

func TestAConnectErrorIsRetriedWithAWaitAndAnAuthErrorOfConnectWaitsForTheLogin(t *testing.T) {
	cn := &fakeConnector{errs: []error{errors.New("the tunnel did not come up"), chat.ErrAuth}}
	e, _, sub := start(t, cn, nil)
	ev := waitStatus(t, sub, chat.StatusDisturbed)
	if !strings.Contains(ev.Status.Reason, "tunnel") || ev.Status.RetryIn < 0 {
		t.Errorf("%+v", ev.Status)
	}
	waitStatus(t, sub, chat.StatusAuth)
	e.Retry("dev")
	if cn.conn(0) == nil {
		t.Fatal("no connection after the login")
	}
}

func TestAFloodFromOneSenderIsDroppedAndCountedAndTooManyNewTopicsGoOnlyToAll(t *testing.T) {
	cn := &fakeConnector{}
	e, st, sub := start(t, cn, nil)
	c := cn.conn(0)
	c.out <- `{"event":"ready"}`
	waitStatus(t, sub, chat.StatusConnected)
	for i := 0; i < 400; i++ {
		c.out <- fmt.Sprintf(`{"event":"message","from":"w1","to":"operator","via":"out","type":"msg","text":"m%d"}`, i)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		kept, _ := st.Tail("dev", "rechner__w1", 1000)
		if len(kept)+e.Snapshot("dev", nil).Status.Dropped == 400 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// the clock may have turned a second on the way: at most a few seconds of the limit are taken
	got, _ := st.Tail("dev", "rechner__w1", 1000)
	if len(got) < chat.MaxLinesPerSenderSecond || len(got) >= 400 || e.Snapshot("dev", nil).Status.Dropped != 400-len(got) {
		t.Errorf("%d kept, %d dropped", len(got), e.Snapshot("dev", nil).Status.Dropped)
	}
}

func TestAMachineThatIsNoMachineOfTheGroupMakesNoConversation(t *testing.T) {
	cn := &fakeConnector{}
	_, st, sub := start(t, cn, nil)
	c := cn.conn(0)
	c.out <- `{"event":"ready"}`
	waitStatus(t, sub, chat.StatusConnected)
	c.out <- `{"event":"message","from":"w1","to":"ghost","via":"out","type":"msg","text":"to nobody"}`
	ev := next(t, sub, "message")
	if len(ev.Keys) != 1 || ev.Keys[0] != "alle" || !ev.Record.Unknown {
		t.Errorf("%+v", ev)
	}
	if cs, _ := st.Conversations("dev"); len(cs) != 1 || cs[0].Key != "alle" {
		t.Errorf("%+v", cs)
	}
}

func TestALineOfTheOperatorIsOutAndPresenceOfAStrangerIsNoState(t *testing.T) {
	cn := &fakeConnector{}
	e, _, sub := start(t, cn, nil)
	c := cn.conn(0)
	c.out <- `{"event":"message","from":"ghost","via":"presence","type":"presence","state":"up"}`
	c.out <- `{"event":"message","from":"operator","to":"w1","via":"out","type":"msg","text":"from another terminal"}`
	ev := next(t, sub, "message")
	if ev.Record.Dir != "out" {
		t.Errorf("dir %q", ev.Record.Dir)
	}
	if s := e.Snapshot("dev", nil); len(s.Machines) != 3 || s.Machines["w1"].State != "unknown" {
		t.Errorf("%+v", s.Machines)
	}
}

func TestAPageThatDoesNotReadIsToldToAskAgain(t *testing.T) {
	cn := &fakeConnector{}
	var tick atomic.Int64 // a clock that runs fast: the limit of lines per second does not get in the way
	base := time.Now()
	e := chat.New(chat.Options{Store: chatlog.Memory(0), Connector: cn, Wait: 20 * time.Millisecond, Now: func() time.Time { return base.Add(time.Duration(tick.Add(1)) * time.Second) }})
	sub := e.Subscribe()
	t.Cleanup(e.Stop)
	e.Ensure("dev", grp())
	c := cn.conn(0)
	for i := 0; i < 300; i++ { // more than the page takes
		c.out <- `{"event":"message","from":"w1","to":"operator","via":"out","type":"msg","text":"x"}`
	}
	deadline := time.After(5 * time.Second)
	for len(sub.C) < cap(sub.C) {
		select {
		case <-deadline:
			t.Fatal("the channel does not fill")
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond)
	for len(sub.C) > 0 { // the page reads again
		<-sub.C
	}
	c.out <- `{"event":"message","from":"w1","to":"operator","via":"out","type":"msg","text":"y"}`
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == "resync" {
				return
			}
		case <-deadline:
			t.Fatal("no resync")
		}
	}
}

func TestAGroupThatIsNotRunningIsDownNotDisturbedAndIsTriedAgain(t *testing.T) {
	cn := &fakeConnector{errs: []error{fmt.Errorf("%w: no machine of the group has an SSH config: run viper up first", chat.ErrNotRunning)}}
	e, _, sub := start(t, cn, nil)
	ev := waitStatus(t, sub, chat.StatusDown)
	if ev.Status.Reason == "" || strings.Contains(ev.Status.Status, "disturbed") {
		t.Errorf("%+v", ev.Status)
	}
	if got := e.Snapshot("dev", nil).Status.Status; got != chat.StatusDown && got != chat.StatusConnecting && got != chat.StatusConnected {
		t.Errorf("status %s", got)
	}
	cn.conn(0).out <- `{"event":"ready","me":"operator","group":"dev"}` // the next try finds it up
	waitStatus(t, sub, chat.StatusConnected)
}
