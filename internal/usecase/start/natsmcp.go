package start

import (
	"bytes"
	"context"
	"debug/elf"
	"fmt"
	"os"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/mcpbin"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

// MCP paths on the machine.
const (
	natsMCPBinary = natsserver.MCPBinary
	natsMCPEnv    = "mcp.env"
)

// natsMCP installs the NATS MCP server on the machine and registers it with Claude Code
// (docs/plan/nats-mcp.md). It runs after the Claude Code login, when the machine has joined its
// group. Without NATS_MCP it does nothing. It never starts Claude Code and never confirms the
// dialog of the channel flag: the command is only shown.
func (r *runner) natsMCP(ctx context.Context) (int, error) {
	c := r.c
	if !c.NATS.Enabled || !c.NATS.MCP {
		return 0, nil
	}
	fail := func(what string, err error) (int, error) {
		return r.fail("NATS MCP: %s failed:\n%s", what, err)
	}
	bin, err := r.natsMCPBinary(ctx)
	if err != nil {
		return r.fail("NATS MCP: %v", err)
	}
	if _, err := r.remote(ctx, string(bin), natsMCPInstallScript, natsserver.Dir, mcpbin.Checksum(bin)); err != nil {
		return fail("installing the server", err)
	}
	env := "VIPER_NATS_MCP_PUSH_FROM=" + strings.Join(c.NATS.MCPPushFrom, ",") + "\n" +
		"VIPER_NATS_MCP_TOPICS=" + strings.Join(c.NATS.MCPTopics, ",") + "\n"
	if _, err := r.remote(ctx, env, natsFileScript, natsserver.Dir, natsMCPEnv, "600"); err != nil {
		return fail("writing mcp.env", err)
	}
	allow := strings.Join(model.MCPAllowedTools(c.NATS.MCPAutoAllow), ",")
	if _, err := r.remote(ctx, "", natsMCPClaudeScript, natsMCPBinary, allow); err != nil {
		// exit status 2: only the permissions could not be set (jq is missing)
		if !strings.Contains(err.Error(), "jq is missing") {
			return fail("registering with Claude Code", err)
		}
		r.warnf(ctx, "WARNING: NATS MCP: jq is missing on the machine, so the tools were not allowed; Claude Code will ask.\n")
	}
	if _, err := r.remote(ctx, "", natsMCPTestScript, natsMCPBinary); err != nil {
		return fail("the test of the server", err)
	}
	r.printf("NATS MCP of %s is ready.\n", c.Name)
	r.natsMCPHint()
	return 0, nil
}

// natsMCPBinary is the binary for the architecture of the machine: the file named by NATS_MCP_BINARY
// or the one built into viper.
func (r *runner) natsMCPBinary(ctx context.Context) ([]byte, error) {
	arch, err := r.remote(ctx, "", `uname -m`)
	if err != nil {
		return nil, fmt.Errorf("reading the architecture: %w", err)
	}
	rel, err := natsserver.ReleaseFor(strings.TrimSpace(arch))
	if err != nil {
		return nil, err
	}
	if f := r.c.NATS.MCPBinary; f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("NATS_MCP_BINARY: %w", err)
		}
		if err := checkLinuxBinary(b, rel.Arch); err != nil {
			return nil, fmt.Errorf("NATS_MCP_BINARY %s: %w", f, err)
		}
		return b, nil
	}
	return mcpbin.Binary(rel.Arch)
}

// checkLinuxBinary says whether data is a Linux program for the architecture ("amd64" or "arm64"): a file of
// another architecture would replace the one that works and fail on the machine.
func checkLinuxBinary(data []byte, goarch string) error {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("not a Linux program (ELF): %w", err)
	}
	defer func() { _ = f.Close() }() // a reader over memory
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[goarch]
	if f.Class != elf.ELFCLASS64 || f.Machine != want || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("it is for %s %s, the machine is %s", f.Class, f.Machine, goarch)
	}
	return nil
}

// natsMCPHint says how Claude Code gets the messages of other machines pushed into the session.
// viper does not do it: the flag asks the user to confirm, and that stays with the user.
func (r *runner) natsMCPHint() {
	if len(r.c.NATS.MCPPushFrom) == 0 {
		r.printf("The tools of the server are there in every Claude Code session on the machine. NATS_MCP_PUSH_FROM is empty, so no message is pushed into a session.\n")
		return
	}
	r.printf("Messages of %s are pushed into a session that is started with the channel flag (you confirm its dialog yourself):\n  claude --dangerously-load-development-channels server:nats\n", strings.Join(r.c.NATS.MCPPushFrom, ", "))
}
