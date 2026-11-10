package cli

import (
	"fmt"
	"io"
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

// writeMCPBinary writes the viper-nats-mcp program for the architecture to out, so it can be saved
// (viper mcp-binary --arch arm64 > file) and named in NATS_MCP_BINARY or looked at.
func writeMCPBinary(o *options, out io.Writer, terminal bool) error {
	arch, err := mcpArch(o.arch)
	if err != nil {
		return err
	}
	if terminal {
		return fmt.Errorf("viper mcp-binary: the program is binary: redirect it into a file (viper mcp-binary --arch %s > viper-nats-mcp)", arch)
	}
	fn := o.host().Services.MCPBinary
	if fn == nil {
		return fmt.Errorf("viper mcp-binary: this viper has no MCP server built in")
	}
	b, err := fn(arch)
	if err != nil {
		return err
	}
	_, err = out.Write(b)
	return err
}

func nativeMCPBinary(o *options) error {
	return writeMCPBinary(o, o.host().Stdout, o.host().IsTerminal())
}
