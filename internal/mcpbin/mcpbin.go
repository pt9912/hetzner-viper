// Package mcpbin holds the viper-nats-mcp binaries that viper puts on the machines of a NATS group
// (docs/plan/nats-mcp.md): one for each architecture of a server, built into viper by make (the
// target mcp writes them into the directory bin before viper is built). A viper built with a plain
// go build has none of them; the functions then say so, and NATS_MCP_BINARY names a file instead.
package mcpbin

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
)

//go:embed all:bin
var files embed.FS

// ErrNotIncluded is returned for an architecture whose binary was not built into this viper.
var ErrNotIncluded = errors.New("this viper was built without the NATS MCP server (build it with make, or name a file with NATS_MCP_BINARY)")

// Architectures are the Go names of the architectures of the servers viper creates.
func Architectures() []string { return []string{"amd64", "arm64"} }

// FileName is the name of the binary of an architecture, in the directory bin and in the release
// scripts.
func FileName(goarch string) string { return "viper-nats-mcp_linux_" + goarch }

// HostFileName is the name of the binary for the system goos/goarch (a Linux server, or the computer viper runs on): the file
// of a Windows program ends in .exe.
func HostFileName(goos, goarch string) string {
	name := "viper-nats-mcp_" + goos + "_" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Host returns the viper-nats-mcp binary for the system this viper runs on (it is the same build as the one that is written to
// the servers). A viper built without it (plain go build) returns ErrNotIncluded.
func Host() ([]byte, error) {
	b, err := files.ReadFile("bin/" + HostFileName(runtime.GOOS, runtime.GOARCH))
	if err != nil || len(b) == 0 {
		return nil, ErrNotIncluded
	}
	return b, nil
}

// Binary returns the viper-nats-mcp binary for a Linux server of that architecture ("amd64" or
// "arm64"). An empty file counts as not included.
func Binary(goarch string) ([]byte, error) {
	known := false
	for _, a := range Architectures() {
		known = known || a == goarch
	}
	if !known {
		return nil, fmt.Errorf("no viper-nats-mcp for the architecture %q", goarch)
	}
	b, err := files.ReadFile("bin/" + FileName(goarch))
	if err != nil || len(b) == 0 {
		return nil, ErrNotIncluded
	}
	return b, nil
}

// Checksum is the SHA-256 of a binary as hex, the value viper checks on the machine after it wrote
// the file there.
func Checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
