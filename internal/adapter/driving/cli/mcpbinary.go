package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// mcpArch reads --arch of viper mcp-binary: the Go and the usual names of the two architectures.
func mcpArch(v string) (string, error) {
	switch v {
	case "amd64", "x86_64", "x86":
		return "amd64", nil
	case "arm64", "aarch64", "arm":
		return "arm64", nil
	}
	return "", fmt.Errorf("viper mcp-binary: --arch is amd64 or arm64: %q", v)
}

// writeMCPBinary writes the viper-nats-mcp program to out: for the architecture of a Linux server (--arch), or for the computer
// viper runs on (--host), so it can be saved (viper mcp-binary --arch arm64 > file), named in NATS_MCP_BINARY, or started by Claude Code.
func writeMCPBinary(o *options, out io.Writer, terminal bool) error {
	b, err := mcpProgram(o)
	if err != nil {
		return err
	}
	if o.out != "" { // a file of its own: the program must run, and no other program may be replaced by accident
		return writeProgram(o.out, b)
	}
	if terminal {
		return fmt.Errorf("viper mcp-binary: the program is binary: redirect it into a file or name one with --out")
	}
	_, err = out.Write(b)
	return err
}

// mcpProgram is the program that --arch or --host asks for.
func mcpProgram(o *options) ([]byte, error) {
	if o.hostBin {
		if o.arch != "" {
			return nil, errors.New("viper mcp-binary: --host and --arch exclude each other")
		}
		fn := o.host().Services.MCPHost
		if fn == nil {
			return nil, errors.New("viper mcp-binary: this viper has no MCP server built in")
		}
		return fn()
	}
	arch, err := mcpArch(o.arch)
	if err != nil {
		return nil, err
	}
	fn := o.host().Services.MCPBinary
	if fn == nil {
		return nil, errors.New("viper mcp-binary: this viper has no MCP server built in")
	}
	return fn(arch)
}

// writeProgram writes b to path, replacing a file that is there, executable for the user (a .exe runs under Windows without it).
func writeProgram(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil { //nolint:gosec // a program: the user must run it
		_ = os.Remove(tmp)
		return err
	}
	// a running program is replaced, not overwritten; Windows refuses to replace one that runs: end Claude Code first
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w (is the program running? end the Claude Code session that uses it and try again)", err)
	}
	return nil
}

func nativeMCPBinary(o *options) error {
	return writeMCPBinary(o, o.host().Stdout, o.host().IsTerminal())
}
