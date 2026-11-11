package natsmcp

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Config is what the server needs. It comes from connection.env (written by viper for every
// machine of a group) and mcp.env (the settings of this server). The files are read as plain
// KEY=VALUE lines, never run by a shell. The seed stays in its file.
type Config struct {
	URL      string
	CAFile   string
	SeedFile string
	Names    Names
	PushFrom map[string]bool
	Topics   []string // subscribed at the start and not to be unsubscribed (VIPER_NATS_MCP_TOPICS)

	MaxText     int // the longest text of a message in bytes
	PushPerMin  int // messages per sender and minute that are pushed; more go only to the inbox
	InboxMax    int
	InboxPerMan int
	LogFile     string
	// Quiet: the server connects but says nothing in the group (no presence, no goodbye); viper's test of the
	// installation runs it so, to not make the machine disappear for a Claude Code session that runs already.
	Quiet bool
}

// Defaults of the limits (docs/plan/nats-mcp.md §3).
const (
	DefaultMaxText    = 16 << 10
	DefaultPushPerMin = 10
	DefaultInboxMax   = 200
)

func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if i := strings.Index(v, " #"); i >= 0 { // a comment after the value
			v = v[:i]
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out, sc.Err()
}

// LoadConfig reads connection.env and, if it exists, mcp.env from dir.
func LoadConfig(dir string) (Config, error) {
	conn, err := parseEnvFile(filepath.Join(dir, "connection.env"))
	if err != nil {
		return Config{}, fmt.Errorf("connection.env: %w (has this machine joined its NATS group? viper up)", err)
	}
	mcp, err := parseEnvFile(filepath.Join(dir, "mcp.env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("mcp.env: %w", err)
	}
	names, err := ParsePrefix(conn["VIPER_NATS_SUBJECT_PREFIX"])
	if err != nil {
		return Config{}, err
	}
	c := Config{URL: conn["NATS_URL"], CAFile: conn["NATS_CA_FILE"], SeedFile: conn["NATS_NKEY_SEED_FILE"], Names: names,
		PushFrom: map[string]bool{}, MaxText: DefaultMaxText, PushPerMin: DefaultPushPerMin,
		InboxMax: DefaultInboxMax, InboxPerMan: DefaultInboxMax / 4, LogFile: filepath.Join(dir, "mcp.log")}
	if c.URL == "" || c.SeedFile == "" {
		return Config{}, errors.New("connection.env needs NATS_URL and NATS_NKEY_SEED_FILE")
	}
	for _, m := range strings.Split(mcp["VIPER_NATS_MCP_PUSH_FROM"], ",") {
		if m = strings.TrimSpace(m); m != "" {
			if !ValidName(m) {
				return Config{}, fmt.Errorf("mcp.env: %q in VIPER_NATS_MCP_PUSH_FROM is not a machine name", m)
			}
			c.PushFrom[m] = true
		}
	}
	for _, t := range strings.Split(mcp["VIPER_NATS_MCP_TOPICS"], ",") {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(c.Topics, t) {
			if !ValidName(t) {
				return Config{}, fmt.Errorf("mcp.env: %q in VIPER_NATS_MCP_TOPICS is not a topic name", t)
			}
			c.Topics = append(c.Topics, t)
		}
	}
	if len(c.Topics) > maxTopics {
		return Config{}, fmt.Errorf("mcp.env: at most %d topics in VIPER_NATS_MCP_TOPICS", maxTopics)
	}
	return c, nil
}
