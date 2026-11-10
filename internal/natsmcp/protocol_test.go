package natsmcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

type nopLog struct{}

func (nopLog) Printf(string, ...any) {}

type rig struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan map[string]any
	srv   *natsmcp.Server
	done  chan error
}

func newRig(t *testing.T, tools ...natsmcp.Tool) *rig {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	r := &rig{t: t, in: inW, lines: make(chan map[string]any, 64), done: make(chan error, 1)}
	r.srv = natsmcp.NewServer("test", "1", "instructions", tools, outW, nopLog{})
	go func() { r.done <- r.srv.Serve(context.Background(), inR); _ = outW.Close() }()
	go func() {
		sc := bufio.NewReader(outR)
		for {
			line, err := sc.ReadBytes('\n')
			if err != nil {
				close(r.lines)
				return
			}
			var m map[string]any
			if json.Unmarshal(line, &m) != nil {
				t.Errorf("not JSON on stdout: %q", line)
				continue
			}
			r.lines <- m
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return r
}

// newInitializedRig is a rig whose client has sent initialize (tools are only served after that).
func newInitializedRig(t *testing.T, tools ...natsmcp.Tool) *rig {
	t.Helper()
	r := newRig(t, tools...)
	r.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if m := r.read(); m["id"] != float64(0) {
		t.Fatalf("initialize: %v", m)
	}
	return r
}

func (r *rig) send(s string) {
	r.t.Helper()
	if _, err := io.WriteString(r.in, s+"\n"); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) read() map[string]any {
	r.t.Helper()
	select {
	case m, ok := <-r.lines:
		if !ok {
			r.t.Fatal("output closed")
		}
		return m
	case <-time.After(3 * time.Second):
		r.t.Fatal("no answer")
		return nil
	}
}

func (r *rig) silent() {
	r.t.Helper()
	select {
	case m := <-r.lines:
		r.t.Fatalf("unexpected output: %v", m)
	case <-time.After(150 * time.Millisecond):
	}
}

func result(m map[string]any) map[string]any { r, _ := m["result"].(map[string]any); return r }
func errCode(m map[string]any) int {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return int(c)
}

func TestTheVersionIsOnlyAgreedToIfWeKnowIt(t *testing.T) {
	r := newRig(t)
	for asked, want := range map[string]string{"2026-07-28": "2025-11-25", "2025-06-18": "2025-06-18", "1999-01-01": "2025-11-25", "2024-11-05": "2024-11-05"} {
		r.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + asked + `"}}`)
		res := result(r.read())
		if res["protocolVersion"] != want {
			t.Errorf("asked %s, answered %v, want %s", asked, res["protocolVersion"], want)
		}
		caps := res["capabilities"].(map[string]any)
		if _, ok := caps["tools"]; !ok {
			t.Error("no tools capability")
		}
		if _, ok := caps["experimental"].(map[string]any)["claude/channel"]; !ok {
			t.Error("not declared as a channel")
		}
		if res["instructions"] != "instructions" {
			t.Error("instructions")
		}
	}
}

func TestNotificationsAreNeverAnsweredAndUnknownMethodsAre(t *testing.T) {
	r := newRig(t)
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	r.send(`{"jsonrpc":"2.0","method":"notifications/whatever","params":{}}`)
	r.silent()
	r.send(`{"jsonrpc":"2.0","id":7,"method":"nope"}`)
	if m := r.read(); errCode(m) != -32601 || m["id"] != float64(7) {
		t.Errorf("%v", m)
	}
	r.send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if m := r.read(); m["id"] != float64(8) || result(m) == nil {
		t.Errorf("%v", m)
	}
}

func TestBadLinesGetAParseErrorAndDoNotStopTheServer(t *testing.T) {
	r := newRig(t)
	r.send(`this is not json`)
	if m := r.read(); errCode(m) != -32700 || m["id"] != nil {
		t.Errorf("%v", m)
	}
	// a line longer than the limit is skipped to its end; the next one is read
	r.send(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", 5<<20) + `"}}`)
	if m := r.read(); errCode(m) != -32700 {
		t.Errorf("a line over 4 MiB: %v", m)
	}
	r.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if m := r.read(); m["id"] != float64(2) {
		t.Errorf("%v", m)
	}
}

func TestToolsAreListedCalledAndTheirErrorsAreResultsNotProtocolErrors(t *testing.T) {
	r := newInitializedRig(t,
		natsmcp.Tool{Name: "echo", Description: "d", Schema: map[string]any{"type": "object"}, Run: func(_ context.Context, a json.RawMessage) (string, error) { return "echo " + string(a), nil }},
		natsmcp.Tool{Name: "boom", Description: "d", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("it failed") }},
		natsmcp.Tool{Name: "big", Description: "d", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage) (string, error) { return strings.Repeat("x", 100<<10), nil }},
	)
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := result(r.read())["tools"].([]any)
	if len(tools) != 3 || tools[0].(map[string]any)["name"] != "echo" || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Errorf("%v", tools)
	}
	r.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`)
	res := result(r.read())
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != false || !strings.Contains(text, `{"a":1}`) {
		t.Errorf("%v", res)
	}
	r.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"boom"}}`)
	res = result(r.read())
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "it failed") {
		t.Errorf("an error of a tool is a result with isError: %v", res)
	}
	r.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nobody"}}`)
	if m := r.read(); errCode(m) != -32602 {
		t.Errorf("%v", m)
	}
	r.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"big"}}`)
	big := result(r.read())["content"].([]any)[0].(map[string]any)["text"].(string)
	if len(big) > 65<<10 || !strings.Contains(big, "cut") {
		t.Errorf("a result is cut at 64 KB: %d", len(big))
	}
}

func TestAWaitingCallDoesNotBlockOthersAndCanBeCancelled(t *testing.T) {
	started := make(chan struct{})
	r := newInitializedRig(t, natsmcp.Tool{Name: "wait", Description: "d", Schema: map[string]any{"type": "object"}, Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
		close(started)
		<-ctx.Done()
		return "", errors.New("cancelled")
	}})
	r.send(`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"wait"}}`)
	<-started
	r.send(`{"jsonrpc":"2.0","id":11,"method":"ping"}`)
	if m := r.read(); m["id"] != float64(11) {
		t.Fatalf("ping must be answered while a call waits: %v", m)
	}
	r.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":10}}`)
	// a cancelled request is not answered (the client has forgotten it): the next answer is the one to the ping
	time.Sleep(100 * time.Millisecond)
	r.send(`{"jsonrpc":"2.0","id":12,"method":"ping"}`)
	if m := r.read(); m["id"] != float64(12) {
		t.Errorf("nothing may come for the cancelled call: %v", m)
	}
}

func TestChannelNotificationsComeOnlyAfterInitializedAndKeepOnlyIdentifierKeys(t *testing.T) {
	r := newRig(t)
	if err := r.srv.Notify("early", nil); err == nil {
		t.Error("nothing is pushed before the client says it is initialized")
	}
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	time.Sleep(100 * time.Millisecond)
	if err := r.srv.Notify("hello <b>", map[string]string{"from": "alpha", "bad-key": "x", "ts": "now", "a b": "y", "": "z"}); err != nil {
		t.Fatal(err)
	}
	m := r.read()
	p := m["params"].(map[string]any)
	meta := p["meta"].(map[string]any)
	if m["method"] != "notifications/claude/channel" || m["id"] != nil || p["content"] != "hello <b>" || len(meta) != 2 || meta["from"] != "alpha" || meta["ts"] != "now" {
		t.Errorf("%v", m)
	}
}

func TestTheServerEndsWhenStdinEndsAndWaitsForCalls(t *testing.T) {
	r := newInitializedRig(t, natsmcp.Tool{Name: "slow", Description: "d", Schema: map[string]any{"type": "object"}, Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
		<-ctx.Done()
		return "", errors.New("cancelled")
	}})
	r.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}`)
	time.Sleep(50 * time.Millisecond)
	_ = r.in.Close()
	select {
	case err := <-r.done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the server did not end")
	}
}

func TestToolsAreNotServedBeforeInitialize(t *testing.T) {
	r := newRig(t, natsmcp.Tool{Name: "x", Description: "d", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage) (string, error) { return "ran", nil }})
	for _, req := range []string{`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"x"}}`} {
		r.send(req)
		m := r.read()
		e, _ := m["error"].(map[string]any)
		if e == nil || e["code"] != float64(-32002) {
			t.Errorf("%s: %v", req, m)
		}
	}
	r.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if m := r.read(); m["id"] != float64(3) || m["error"] != nil {
		t.Errorf("ping needs no initialize: %v", m)
	}
}
