// viper-nats-mcp is the MCP server for the NATS group of a machine (docs/plan/nats-mcp.md).
package main

import (
	"os"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

// set at build time: -ldflags "-X main.version=v0.16.0"
var version = "dev"

func main() {
	os.Exit(natsmcp.Main(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
