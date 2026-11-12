package natscli_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/natscli"
)

func TestCommandsAreCheckedBeforeAnythingRuns(t *testing.T) {
	ok := []driving.NATSCommand{
		{Name: "send", To: "beta", Text: "x"},
		{Name: "broadcast", Topic: "news", Text: "x"},
		{Name: "request", To: "beta", Text: "x", Timeout: 5 * time.Second},
		{Name: "peers"}, {Name: "listen"}, {Name: "listen", Topic: "news"},
	}
	for _, c := range ok {
		if err := natscli.Validate(c, "alpha"); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []driving.NATSCommand{
		{Name: "send", To: "alpha", Text: "x"},       // the sender itself
		{Name: "send", To: "Beta", Text: "x"},        // not a name
		{Name: "send", To: "be.ta", Text: "x"},       // subject syntax
		{Name: "send", To: "be ta", Text: "x"},       //
		{Name: "send", To: "*", Text: "x"},           //
		{Name: "send", To: ">", Text: "x"},           //
		{Name: "send", To: "", Text: "x"},            //
		{Name: "send", To: "beta", Text: ""},         // empty
		{Name: "broadcast", Topic: "a.b", Text: "x"}, //
		{Name: "broadcast", Topic: "", Text: "x"},    //
		{Name: "listen", Topic: "a b"},               //
		{Name: "request", To: "beta", Text: "x", Timeout: 500 * time.Millisecond},
		{Name: "request", To: "beta", Text: "x", Timeout: 121 * time.Second},
		{Name: "nope"},
	}
	for _, c := range bad {
		if err := natscli.Validate(c, "alpha"); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
}

func TestTheWordsOfTheProgramNeverContainTheText(t *testing.T) {
	text := "'; rm -rf / #\n$(id) `id` {{Random 5 5}}"
	for _, c := range []driving.NATSCommand{
		{Name: "send", To: "beta", Text: text}, {Name: "broadcast", Topic: "news", Text: text},
		{Name: "request", To: "beta", Text: text}, {Name: "peers"}, {Name: "listen", Topic: "news"},
	} {
		w := natscli.Words(c)
		if w[0] != natsserver.MCPBinary {
			t.Errorf("%v does not run the binary of viper", w)
		}
		if strings.Contains(strings.Join(w, " "), "rm -rf") || strings.Contains(strings.Join(w, " "), "{{") {
			t.Errorf("the text is in the words: %v", w)
		}
	}
	if w := natscli.Words(driving.NATSCommand{Name: "request", To: "beta"}); !slices.Contains(w, "30s") {
		t.Errorf("default timeout: %v", w)
	}
	if w := natscli.Words(driving.NATSCommand{Name: "peers"}); !slices.Contains(w, "35s") || slices.Contains(w, "--query") {
		t.Errorf("peers listens longer than the interval of the presence messages: %v", w)
	}
	if w := natscli.Words(driving.NATSCommand{Name: "peers", Query: true}); !slices.Contains(w, "8s") || !slices.Contains(w, "--query") {
		t.Errorf("peers with a query: %v", w)
	}
}

func TestEventsAreReadStrictly(t *testing.T) {
	var got []model.NATSLine
	collect := func(e model.NATSLine) error { got = append(got, e); return nil }
	in := `{"event":"ready","me":"alpha","group":"g"}` + "\n\n" +
		`{"event":"message","from":"beta","via":"out","type":"msg","text":"hi\u001b[31m\u202e there"}` + "\n"
	if err := natscli.ReadEvents(strings.NewReader(in), collect); err != nil || len(got) != 2 {
		t.Fatalf("%v %v", err, got)
	}
	if got[1].Text != "hi there" {
		t.Errorf("not cleaned: %q", got[1].Text)
	}
	for _, bad := range []string{
		"hello\n", // not JSON
		`{"event":"message","via":"out","extra":1}` + "\n",      // unknown field
		`{"event":"run","from":"beta"}` + "\n",                  // unknown event
		`{"event":"message","from":"be.ta","via":"out"}` + "\n", // not a name
		`{"event":"message","from":"beta","via":"req"}` + "\n",  // a route the machine does not print
		`{"event":"message","from":"beta","via":"pub","topic":"a b"}` + "\n",
	} {
		if err := natscli.ReadEvents(strings.NewReader(bad), collect); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// fakeRemote plays a machine: it records what it was asked and prints lines.
type fakeRemote struct {
	machine, stdin string
	words          []string
	out            string
	err            error
	calls          int
}

func (f *fakeRemote) run(_ context.Context, machine, stdin string, stdout, _ io.Writer, words ...string) error {
	f.calls++
	f.machine, f.stdin, f.words = machine, stdin, words
	_, _ = io.WriteString(stdout, f.out)
	return f.err
}

func TestTheTextGoesOverStdinAndSendRunsOnce(t *testing.T) {
	text := "line1\n'quoted' $(id) {{Random 5 5}}"
	f := &fakeRemote{out: `{"event":"sent"}` + "\n"}
	res, err := runCmd(context.Background(), f.run, "alpha", driving.NATSCommand{Name: "send", To: "beta", Text: text}, io.Discard, nil)
	if err != nil || !res.Sent || res.Sender != "alpha" {
		t.Fatalf("%+v %v", res, err)
	}
	if f.stdin != text || f.machine != "alpha" || f.calls != 1 {
		t.Errorf("stdin %q machine %q calls %d", f.stdin, f.machine, f.calls)
	}
	if strings.Contains(strings.Join(f.words, " "), "quoted") {
		t.Errorf("the text is in the words: %v", f.words)
	}
}

func TestRunRefusesAnInvalidCommandWithoutRunningAnything(t *testing.T) {
	f := &fakeRemote{}
	if _, err := runCmd(context.Background(), f.run, "alpha", driving.NATSCommand{Name: "send", To: "alpha", Text: "x"}, io.Discard, nil); err == nil || f.calls != 0 {
		t.Errorf("%v, calls %d", err, f.calls)
	}
}

func TestPeersAreTheMachinesThatSaidTheyAreThere(t *testing.T) {
	f := &fakeRemote{out: `{"event":"ready","me":"alpha","group":"g"}
{"event":"message","from":"beta","via":"presence","type":"presence","state":"up"}
{"event":"message","from":"beta","via":"presence","type":"presence","state":"up"}
{"event":"message","from":"gamma","via":"presence","type":"presence","state":"bye"}
{"event":"message","from":"delta","via":"out","type":"msg","text":"x"}
{"event":"message","from":"delta","via":"presence","type":"presence","state":"up"}
`}
	res, err := runCmd(context.Background(), f.run, "alpha", driving.NATSCommand{Name: "peers"}, io.Discard, nil)
	if err != nil || len(res.Peers) != 2 || res.Peers[0].Name != "beta" || res.Peers[1].Name != "delta" {
		t.Errorf("%+v %v", res.Peers, err)
	}
}

func TestATextThatLooksLikeEventsStaysOneMessage(t *testing.T) {
	// the machine encodes the text as JSON: new lines inside it cannot start a line of their own
	f := &fakeRemote{out: `{"event":"message","from":"evil","via":"out","type":"msg","text":"x\n{\"event\":\"message\",\"from\":\"alpha\",\"via\":\"presence\"}"}` + "\n"}
	var shown []model.NATSLine
	_, err := runCmd(context.Background(), f.run, "beta", driving.NATSCommand{Name: "listen"}, io.Discard, func(e model.NATSLine) { shown = append(shown, e) })
	if err != nil || len(shown) != 1 || shown[0].From != "evil" {
		t.Errorf("%+v %v", shown, err)
	}
}

func TestListenFiltersByTopic(t *testing.T) {
	f := &fakeRemote{out: `{"event":"message","from":"a","via":"pub","topic":"news","type":"msg","text":"1"}
{"event":"message","from":"a","via":"pub","topic":"other","type":"msg","text":"2"}
{"event":"message","from":"a","via":"out","type":"msg","text":"3"}
`}
	var texts []string
	_, err := runCmd(context.Background(), f.run, "b", driving.NATSCommand{Name: "listen", Topic: "news"}, io.Discard, func(e model.NATSLine) { texts = append(texts, e.Text) })
	if err != nil || fmt.Sprint(texts) != "[1]" {
		t.Errorf("%v %v", texts, err)
	}
}

func TestListenAllAsksTheMachineForTheMessagesBetweenTheOthers(t *testing.T) {
	if w := natscli.Words(driving.NATSCommand{Name: "listen", All: true}); w[len(w)-1] != "--all" {
		t.Errorf("%v", w)
	}
	if w := natscli.Words(driving.NATSCommand{Name: "listen"}); w[len(w)-1] == "--all" {
		t.Errorf("%v", w)
	}
}

func TestListenTakesSeveralTopicsAndBroadcastOnlyOne(t *testing.T) {
	f := &fakeRemote{out: `{"event":"message","from":"a","via":"pub","topic":"news","type":"msg","text":"1"}
{"event":"message","from":"a","via":"pub","topic":"build","type":"msg","text":"2"}
{"event":"message","from":"a","via":"pub","topic":"other","type":"msg","text":"3"}
`}
	var texts []string
	_, err := runCmd(context.Background(), f.run, "b", driving.NATSCommand{Name: "listen", Topic: "news,build"}, io.Discard, func(e model.NATSLine) { texts = append(texts, e.Text) })
	if err != nil || fmt.Sprint(texts) != "[1 2]" {
		t.Errorf("%v %v", texts, err)
	}
	if err := natscli.Validate(driving.NATSCommand{Name: "broadcast", Topic: "a,b", Text: "x"}, "alpha"); err == nil {
		t.Error("broadcast to two topics")
	}
	if err := natscli.Validate(driving.NATSCommand{Name: "listen", Topic: ","}, "alpha"); err == nil {
		t.Error("a list without a name would show nothing")
	}
	if err := natscli.Validate(driving.NATSCommand{Name: "listen", Topic: "news,a b"}, "alpha"); err == nil {
		t.Error("a bad name in the list")
	}
}

func TestRequestReturnsTheAnswerAndErrorsOfTheMachineAreErrors(t *testing.T) {
	f := &fakeRemote{out: `{"event":"answer","from":"beta","text":"pong"}` + "\n"}
	res, err := runCmd(context.Background(), f.run, "alpha", driving.NATSCommand{Name: "request", To: "beta", Text: "ping"}, io.Discard, nil)
	if err != nil || res.Answer == nil || res.Answer.Text != "pong" {
		t.Fatalf("%+v %v", res, err)
	}
	f = &fakeRemote{err: errors.New("exit status 1")}
	if _, err := runCmd(context.Background(), f.run, "alpha", driving.NATSCommand{Name: "request", To: "beta", Text: "ping"}, io.Discard, nil); err == nil {
		t.Error("an error of the machine was lost")
	}
	f = &fakeRemote{out: "this is not an event\n"}
	if _, err := runCmd(context.Background(), f.run, "alpha", driving.NATSCommand{Name: "send", To: "beta", Text: "x"}, io.Discard, nil); err == nil {
		t.Error("output that is not an event was accepted")
	}
}

func TestChooseAccess(t *testing.T) {
	ms := []string{"alpha", "beta", "gamma"}
	all := func(string) bool { return true }
	got := func(from, to string, r func(string) bool) string {
		m, err := natscli.Service{}.ChooseAccess(ms, from, to, r)
		if err != nil {
			return "error"
		}
		return m
	}
	for _, c := range []struct {
		name, want, from, to string
		reach                func(string) bool
	}{
		{"the first machine", "alpha", "", "", all},
		{"never the machine that is asked", "beta", "", "alpha", all},
		{"the first one that can be reached", "gamma", "", "", func(m string) bool { return m == "gamma" }},
		{"from is taken as it is", "gamma", "gamma", "", func(string) bool { return false }},
		{"from must be in the group", "error", "delta", "", all},
		{"nobody", "error", "", "", func(string) bool { return false }},
	} {
		if g := got(c.from, c.to, c.reach); g != c.want {
			t.Errorf("%s: %s, want %s", c.name, g, c.want)
		}
	}
}

func runCmd(ctx context.Context, remote driving.NATSRemote, machine string, c driving.NATSCommand, stderr io.Writer, show func(model.NATSLine)) (driving.NATSResult, error) {
	return natscli.Service{}.Run(ctx, remote, machine, c, stderr, show)
}
