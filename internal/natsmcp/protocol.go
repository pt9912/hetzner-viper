// Package natsmcp is an MCP server (JSON-RPC 2.0 over stdio) that lets the Claude Code agents of
// the machines of a NATS group (see docs/plan/nats-mcp.md) exchange messages. The sender of a
// message is the third token of its subject, which the NATS server enforces; it is never taken
// from the content.
package natsmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Logger is where the server writes what it does (never the content of a message).
type Logger interface {
	Printf(format string, args ...any)
}

// supportedVersions are the protocol revisions the server speaks, newest first. The server never
// agrees to a revision it does not know: Claude Code does not register a channel server that
// negotiates the revision 2026-07-28.
func supportedVersions() []string {
	return []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
}

// maxLine is the longest line the server reads; a longer one is answered with a parse error.
const maxLine = 4 << 20

// maxResult is the longest text a tool returns.
const maxResult = 64 << 10

// Tool is a function the agent can call.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Run         func(ctx context.Context, args json.RawMessage) (string, error)
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Server speaks MCP on stdin and stdout.
type Server struct {
	name, version, instructions string
	tools                       []Tool
	byName                      map[string]Tool
	log                         Logger

	wmu sync.Mutex // stdout carries the protocol only; every writer takes this lock
	out io.Writer

	mu          sync.Mutex
	initialized bool // the client said notifications/initialized
	sawInit     bool // the client sent the request initialize
	running     map[string]context.CancelFunc
	cancelled   map[string]bool // the calls the client cancelled: they are not answered
	wg          sync.WaitGroup
}

// NewServer returns a server that writes its answers to out.
func NewServer(name, version, instructions string, tools []Tool, out io.Writer, log Logger) *Server {
	s := &Server{name: name, version: version, instructions: instructions, tools: tools, out: out, log: log,
		byName: map[string]Tool{}, running: map[string]context.CancelFunc{}, cancelled: map[string]bool{}}
	for _, t := range tools {
		s.byName[t.Name] = t
	}
	return s
}

func (s *Server) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.out.Write(append(b, '\n'))
	return err
}

func (s *Server) reply(id json.RawMessage, result any) {
	if err := s.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result}); err != nil {
		s.log.Printf("writing an answer failed: %v", err)
	}
}

func (s *Server) fail(id json.RawMessage, code int, msg string) {
	if err := s.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}); err != nil {
		s.log.Printf("writing an error failed: %v", err)
	}
}

// Ready tells whether the client has said that it is initialized; channel notifications are sent
// only after that.
func (s *Server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// Notify pushes a message into the session of the agent as a channel notification. The keys of
// meta must be identifiers (letters, digits, underscore); others would be dropped by Claude Code
// and are not sent. It does nothing before the client has initialized the connection.
func (s *Server) Notify(content string, meta map[string]string) error {
	if !s.Ready() {
		return errors.New("the client is not initialized")
	}
	clean := map[string]string{}
	for k, v := range meta {
		if isIdent(k) {
			clean[k] = v
		}
	}
	return s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel",
		"params": map[string]any{"content": content, "meta": clean}})
}

func isIdent(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// readLine reads one line of at most max bytes; a longer line is read to its end and reported as
// too long, so the next line is read correctly.
func readLine(r *bufio.Reader, limit int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			if len(buf) > 0 && errors.Is(err, io.EOF) {
				return buf, tooLong, nil
			}
			return nil, tooLong, err
		}
		if !tooLong {
			if len(buf)+len(part) > limit {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, part...)
			}
		}
		if !isPrefix {
			return buf, tooLong, nil
		}
	}
}

type inLine struct {
	line    []byte
	tooLong bool
	err     error
}

// Serve reads requests from in until it ends or ctx is done, and waits for the running calls. The lines
// are read in a goroutine, so that a signal ends the server even while it waits for the next line.
func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	lines := make(chan inLine)
	done := make(chan struct{})
	defer close(done)
	go func() {
		r := bufio.NewReaderSize(in, 64<<10)
		for {
			line, tooLong, err := readLine(r, maxLine)
			select {
			case lines <- inLine{bytes.Clone(line), tooLong, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var l inLine
		select {
		case <-ctx.Done():
			s.stop()
			return nil
		case l = <-lines:
		}
		if l.err != nil {
			if errors.Is(l.err, io.EOF) {
				s.stop()
				return nil
			}
			s.stop()
			return l.err
		}
		if l.tooLong {
			s.fail(nil, -32700, "line too long")
			continue
		}
		if len(bytes.TrimSpace(l.line)) == 0 {
			continue
		}
		s.handle(ctx, l.line)
	}
}

func (s *Server) stop() {
	s.mu.Lock()
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) handle(ctx context.Context, line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.fail(nil, -32700, "parse error")
		return
	}
	if len(req.ID) == 0 { // a notification: never answered
		s.notification(req)
		return
	}
	if (req.Method == "tools/list" || req.Method == "tools/call") && !s.hasInitialized() {
		s.fail(req.ID, -32002, "the server is not initialized: send initialize first")
		return
	}
	switch req.Method {
	case "initialize":
		s.initialize(req)
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		list := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		s.reply(req.ID, map[string]any{"tools": list})
	case "tools/call":
		s.call(ctx, req)
	default:
		s.fail(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) notification(req rpcRequest) {
	switch req.Method {
	case "notifications/initialized":
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.mu.Lock()
			if cancel, ok := s.running[string(p.RequestID)]; ok {
				s.cancelled[string(p.RequestID)] = true // a cancelled request is not answered
				cancel()
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) hasInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawInit
}

func (s *Server) initialize(req rpcRequest) {
	s.mu.Lock()
	s.sawInit = true
	s.mu.Unlock()
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &p)
	versions := supportedVersions()
	version := versions[0]
	for _, v := range versions {
		if v == p.ProtocolVersion {
			version = v
		}
	}
	s.reply(req.ID, map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}, "experimental": map[string]any{"claude/channel": map[string]any{}}},
		"serverInfo":      map[string]any{"name": s.name, "version": s.version},
		"instructions":    s.instructions,
	})
}

func (s *Server) call(ctx context.Context, req rpcRequest) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.fail(req.ID, -32602, "invalid params")
		return
	}
	tool, ok := s.byName[p.Name]
	if !ok {
		s.fail(req.ID, -32602, "unknown tool: "+p.Name)
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	key := string(req.ID)
	s.mu.Lock()
	s.running[key] = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	// calls run on their own, so a request that waits for an answer does not stop ping or the other tools
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, key)
			s.mu.Unlock()
			cancel()
		}()
		text, err := tool.Run(cctx, p.Arguments)
		s.mu.Lock()
		gone := s.cancelled[key]
		delete(s.cancelled, key)
		s.mu.Unlock()
		if gone {
			return
		}
		isError := err != nil
		if isError {
			text = err.Error()
		}
		if len(text) > maxResult {
			text = text[:maxResult] + "\n[cut: the result was longer than 64 KB]"
		}
		s.reply(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError})
	}()
}

func argError(tool string, err error) error {
	return fmt.Errorf("%s: invalid arguments: %v", tool, err)
}
