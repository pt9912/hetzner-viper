package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

// The MCP server of the machines, run as a second entry of the kind operator (name local), as viper nats mcp will run it on a laptop
// (docs/plan/nats-lokal.md, milestone 0): it connects, answers over the pipes with nothing but protocol on its output, sends, asks,
// takes an answer that comes while it waits and drops one that comes after, and pushes the messages of the machines that are named.

type mcpRig struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan map[string]any
	id    int
	diag  func() string // what to say when the server does not come up
}

func startLocalMCP(t *testing.T, w *chatWorld, pushFrom ...string) *mcpRig {
	t.Helper()
	push := map[string]bool{}
	for _, p := range pushFrom {
		push[p] = true
	}
	cfg := natsmcp.Config{URL: w.url, SeedFile: w.localSeed, Names: natsmcp.Names{Group: "dev", Me: "local"}, PushFrom: push,
		MaxText: natsmcp.DefaultMaxText, PushPerMin: natsmcp.DefaultPushPerMin, InboxMax: natsmcp.DefaultInboxMax, InboxPerMan: natsmcp.DefaultInboxMax / 4,
		AttachDir: t.TempDir()}
	return startRig(t, nil, func(ctx context.Context, in io.Reader, out io.Writer) {
		_ = natsmcp.Run(ctx, cfg, "test", in, out, quietLog{})
	})
}

// startRig runs an MCP server (run) on pipes and speaks to it: the protocol handshake is done when it returns.
func startRig(t *testing.T, diag func() string, run func(ctx context.Context, in io.Reader, out io.Writer)) *mcpRig {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { run(ctx, inR, outW); _ = outW.Close(); close(done) }()
	r := &mcpRig{t: t, in: inW, lines: make(chan map[string]any, 256), diag: diag}
	go func() {
		rd := bufio.NewReader(outR)
		for {
			line, err := rd.ReadBytes('\n')
			if err != nil {
				close(r.lines)
				return
			}
			var m map[string]any
			if json.Unmarshal(line, &m) != nil {
				t.Errorf("not protocol on the output: %q", line) // nothing but JSON lines may be on the output
				continue
			}
			r.lines <- m
		}
	}()
	t.Cleanup(func() { cancel(); _ = inW.Close(); <-done })
	r.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	r.await(func(m map[string]any) bool { return m["id"] == float64(0) })
	r.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return r
}

func (r *mcpRig) send(line string) {
	r.t.Helper()
	wrote := make(chan error, 1)
	go func() { _, err := io.WriteString(r.in, line+"\n"); wrote <- err }()
	select {
	case err := <-wrote:
		if err != nil {
			r.t.Fatal(err)
		}
	case <-time.After(20 * time.Second): // the server reads nothing: it did not come up
		extra := ""
		if r.diag != nil {
			extra = "\n" + r.diag()
		}
		r.t.Fatalf("the server does not read its input%s", extra)
	}
}

func (r *mcpRig) await(match func(map[string]any) bool) map[string]any {
	r.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-r.lines:
			if !ok {
				r.t.Fatal("the output ended")
			}
			if match(m) {
				return m
			}
		case <-deadline:
			r.t.Fatal("the line did not come")
		}
	}
}

// call runs a tool and returns its text (and whether it was an error).
func (r *mcpRig) call(name string, args map[string]any) (string, bool) {
	r.t.Helper()
	r.id++
	id := r.id
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	r.send(string(b))
	m := r.await(func(m map[string]any) bool { return m["id"] == float64(id) })
	res, _ := m["result"].(map[string]any)
	if res == nil {
		r.t.Fatalf("no result: %v", m)
	}
	var text strings.Builder
	for _, c := range res["content"].([]any) {
		text.WriteString(c.(map[string]any)["text"].(string))
	}
	isErr, _ := res["isError"].(bool)
	return text.String(), isErr
}

func TestTheMCPServerRunsAsASecondOperatorEntryAndSendsAndAsksWithNothingButProtocolOnItsOutput(t *testing.T) {
	w := newChatWorld(t)
	r := startLocalMCP(t, w)
	if text, isErr := r.call("send", map[string]any{"to": "beta", "text": "hello from local"}); isErr {
		t.Fatalf("send: %s", text)
	}
	waitFor(t, "beta has it, from local", func() bool {
		in := w.tool(t, "inbox", "{}")
		return strings.Contains(in, "hello from local") && strings.Contains(in, "local")
	})
	// a question that beta answers while local waits
	go func() {
		waitFor(t, "beta has the question", func() bool { return strings.Contains(w.tool(t, "inbox", "{}"), "what time is it") })
		w.tool(t, "reply", `{"handle":2,"text":"noon"}`)
	}()
	text, isErr := r.call("request", map[string]any{"to": "beta", "text": "what time is it", "timeout_seconds": 20})
	if isErr || !strings.Contains(text, "noon") {
		t.Fatalf("request: %v %q", isErr, text)
	}
}

func TestAnAnswerAfterTheWaitIsDroppedAndAResultSentAsAMessageIsKept(t *testing.T) {
	w := newChatWorld(t)
	r := startLocalMCP(t, w)
	// the question is not answered in time
	text, isErr := r.call("request", map[string]any{"to": "beta", "text": "slow task", "timeout_seconds": 1})
	if !isErr && !strings.Contains(text, "no answer") {
		t.Fatalf("a question nobody answers must say so: %q", text)
	}
	waitFor(t, "beta has the question", func() bool { return strings.Contains(w.tool(t, "inbox", "{}"), "slow task") })
	w.tool(t, "reply", `{"handle":1,"text":"late answer"}`) // an answer, after the wait: dropped
	// the result as a message: kept
	if err := w.beta.SendText("local", "the result"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the result is in the inbox of local", func() bool {
		in, _ := r.call("inbox", map[string]any{})
		return strings.Contains(in, "the result")
	})
	in, _ := r.call("inbox", map[string]any{})
	if strings.Contains(in, "late answer") {
		t.Errorf("an answer after the wait must be dropped, not kept: %s", in)
	}
}

func TestOnlyTheMachinesNamedForPushAreShownInTheSessionAsAChannelEvent(t *testing.T) {
	w := newChatWorld(t)
	r := startLocalMCP(t, w, "beta")
	if err := w.beta.SendText("local", "from beta, named"); err != nil {
		t.Fatal(err)
	}
	r.await(func(m map[string]any) bool {
		p, _ := m["params"].(map[string]any)
		return m["method"] == "notifications/claude/channel" && p != nil && strings.Contains(p["content"].(string), "from beta, named")
	})
	if err := w.gamma.SendText("local", "from gamma, not named"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "gamma's message is in the inbox", func() bool {
		in, _ := r.call("inbox", map[string]any{})
		return strings.Contains(in, "from gamma, not named")
	})
	// and nothing came as an event for it
	for {
		select {
		case m := <-r.lines:
			if p, _ := m["params"].(map[string]any); m["method"] == "notifications/claude/channel" && p != nil && strings.Contains(p["content"].(string), "not named") {
				t.Fatalf("a machine that is not named was pushed: %v", m)
			}
		default:
			return
		}
	}
}
