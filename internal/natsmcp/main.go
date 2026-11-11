package natsmcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Main is the program viper-nats-mcp. Claude Code starts it; it reads connection.env and mcp.env
// from $VIPER_NATS_DIR (default ~/.viper-nats), which viper writes, and speaks MCP on stdin and
// stdout. It returns the exit code.
func Main(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintln(stdout, "viper-nats-mcp", version)
		return 0
	}
	dir := os.Getenv("VIPER_NATS_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "viper-nats-mcp:", err)
			return 1
		}
		dir = filepath.Join(home, ".viper-nats")
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		fmt.Fprintln(stderr, "viper-nats-mcp:", err)
		return 1
	}
	if len(args) > 0 && IsCLICommand(args[0]) {
		return cliMain(args[0], cfg, args[1:], stdin, stdout, stderr)
	}
	cfg.Quiet = os.Getenv("VIPER_NATS_MCP_QUIET") == "1"
	log := NewFileLogger(stderr, cfg.LogFile, 1<<20)
	defer log.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("starting %s as %s in the group %s", version, cfg.Names.Me, cfg.Names.Group)
	if err := Run(ctx, cfg, version, stdin, stdout, log); err != nil {
		log.Printf("stopped: %v", err)
		fmt.Fprintln(stderr, "viper-nats-mcp:", err)
		return 1
	}
	return 0
}
