// viper sets up and uses machines in the cloud for Claude Code: viper up web --repo https://example.org/org/web.git
package main

import (
	"os"

	"github.com/pt9912/hetzner-viper/internal/app"
)

// set at build time: -ldflags "-X main.version=v0.4.0"
var version = "dev"

func main() {
	os.Exit(app.Run(version, os.Args[1:], os.Stdout, os.Stderr))
}
