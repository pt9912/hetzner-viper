package cli

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

// What viper ui shows of the settings (docs/plan/ui.md §5): the names of every file, and the value only of the names that are
// on a list of settings that are no credentials and hold nothing private. Everything else is "set". A list that says what is
// safe (and not what is secret) cannot miss the names of a provider that was added later.

func shownNames() map[string]bool {
	return map[string]bool{
		"PROVIDER": true, "DNS_PROVIDER": true, "DNS_DOMAIN": true, "SERVER_TYPE": true, "SERVER_LOCATION": true, "SERVER_IMAGE": true,
		"SERVER_PREFIX": true, "COMPOSE_IMAGE": true, "K8S_IMAGE": true, "K8S_DOCKER": true, "GROUP_MACHINES": true, "WORKSPACE_DIR": true,
		"REPO_URL": true, "REPO_BRANCH": true, "SSH_ALLOWED_CIDRS": true, "AWS_TAGS_SHARED": true,
		"NATS_ENABLED": true, "NATS_GROUP": true, "NATS_PORT": true, "NATS_MCP": true, "NATS_MCP_AUTOALLOW": true, "NATS_MCP_PUSH_FROM": true,
		"NATS_MCP_TOPICS": true, "NATS_OBJECTS": true, "NATS_OBJECTS_MAX": true, "NATS_OBJECTS_FILE_MAX": true, "NATS_OBJECTS_TTL": true,
		"NATS_OPERATOR_NAME": true, "VIPER_HOMEBREW": true, "VIPER_BREW_PACKAGES": true, "VIPER_BREW_TAPS": true, "VIPER_BREW_TRUST": true, "VIPER_BREWFILE": true, "VIPER_POST_CLONE": true, "VIPER_POST_CLONE_ON_ERROR": true, "VIPER_CODE_SERVER": true, "VIPER_SWAP": true, "VIPER_CLAUDE_MODEL": true,
		"VIPER_CLAUDE_MAX_CONTEXT_TOKENS": true,
	}
}

// valueShown says whether the value of a setting is shown: the list above, the settings of a k3d cluster, and the size of a disk.
func valueShown(key string) bool {
	return shownNames()[key] || strings.HasPrefix(key, "K3D_") || strings.HasSuffix(key, "_ROOT_GB")
}

var assignRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// plainValue is the value of a line of a settings file, if the line is one word, or one quoted string, and nothing but a comment
// after it, and no expansion ($, `, \) a shell would make. Anything else (a second assignment after a ';' or a space, text after
// a quote) is not a value that can be shown: it could be the text of another setting.
func plainValue(raw string) (string, bool) {
	s := strings.TrimLeft(raw, " \t")
	if s == "" || s[0] == '#' && len(raw) > len(s) { // X= and X= # note: nothing
		return "", true
	}
	var v, rest string
	var ok bool
	if s[0] == '"' || s[0] == '\'' {
		v, rest, ok = quotedWord(s)
	} else {
		v, rest, ok = bareWord(s)
	}
	if !ok {
		return "", false
	}
	if rest = strings.TrimSpace(rest); rest != "" && rest[0] != '#' {
		return "", false // more words: other assignments, or a command
	}
	if strings.ContainsAny(v, "$`\\") || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return "", false
	}
	return v, true
}

// quotedWord is a "..." or '...' at the start of s and what follows it.
func quotedWord(s string) (v, rest string, ok bool) {
	end := strings.IndexByte(s[1:], s[0])
	if end < 0 {
		return "", "", false // a value over several lines (VIPER_POST_CLONE): this reads one line, and half of a command says nothing
	}
	v, rest = s[1:1+end], s[end+2:]
	return v, rest, rest == "" || rest[0] == ' ' || rest[0] == '\t' // "a"b is one word
}

// bareWord is the word at the start of s up to a space, and what follows it.
func bareWord(s string) (v, rest string, ok bool) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		i = len(s)
	}
	v, rest = s[:i], s[i:]
	return v, rest, !strings.ContainsAny(v, ";&|<>()\"'")
}

// readSettings is the names in a file, in the order they are in, and the value of those that may be shown.
func readSettings(path string) ([]web.Setting, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only
	var out []web.Setting
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	open := byte(0) // the quote of a value over several lines: its lines are no settings
	for sc.Scan() {
		if open != 0 {
			if strings.IndexByte(sc.Text(), open) >= 0 {
				open = 0
			}
			continue
		}
		m := assignRe.FindStringSubmatch(sc.Text())
		if m == nil || strings.HasPrefix(strings.TrimSpace(sc.Text()), "#") {
			continue
		}
		if q := strings.TrimLeft(m[2], " \t"); q != "" && (q[0] == '"' || q[0] == '\'') && strings.IndexByte(q[1:], q[0]) < 0 {
			open = q[0]
		}
		st := web.Setting{Key: m[1], Set: hasValue(m[2])}
		if v, ok := plainValue(m[2]); ok && valueShown(m[1]) && v != "" && !hasUserinfo(v) {
			st.Value, st.Shown = v, true
		}
		out = append(out, st)
	}
	return out, sc.Err()
}

// Settings lists .env.local, groups/*.env and machines/*.env.local of the directory, and the settings that only the
// environment gives.
func (b *uiBackend) Settings(ctx context.Context) ([]web.SettingsFile, error) {
	dir := b.dir()
	files := []web.SettingsFile{}
	add := func(name, path string) {
		sf := web.SettingsFile{Name: name, Settings: []web.Setting{}}
		if st, err := readSettings(path); err != nil {
			if !os.IsNotExist(err) {
				sf.Error = err.Error()
			} else {
				return
			}
		} else {
			sf.Settings = st
		}
		files = append(files, sf)
	}
	add(".env.local", filepath.Join(dir, ".env.local"))
	for _, n := range listNames(filepath.Join(dir, "groups"), ".env") {
		add("groups/"+n+".env", filepath.Join(dir, "groups", n+".env"))
	}
	for _, n := range listNames(filepath.Join(dir, "machines"), ".env.local") {
		add("machines/"+n+".env.local", filepath.Join(dir, "machines", n+".env.local"))
	}
	files = append(files, b.environmentSettings(files))
	return files, ctx.Err()
}

// environmentSettings are the settings of the list (and the secrets) that the environment has and no file names; the value is
// never shown for them: the environment may hold what no file does.
func (b *uiBackend) environmentSettings(files []web.SettingsFile) web.SettingsFile {
	inFile := map[string]bool{}
	for _, f := range files {
		for _, s := range f.Settings {
			inFile[s.Key] = true
		}
	}
	names := secretNames()
	for k := range shownNames() {
		names = append(names, k)
	}
	sort.Strings(names)
	env := web.SettingsFile{Name: "environment", Settings: []web.Setting{}}
	seen := map[string]bool{}
	for _, n := range names {
		if !seen[n] && !inFile[n] && b.env.Getenv(n) != "" {
			env.Settings = append(env.Settings, web.Setting{Key: n})
		}
		seen[n] = true
	}
	return env
}

// hasUserinfo says whether a value names a URL with credentials (https://user:token@host/...) in any of its words (a tap is
// owner/repo=URL): preflight refuses such a REPO_URL, but a file may hold one, and the page then shows only the name.
func hasUserinfo(v string) bool {
	for _, w := range strings.Fields(v) {
		_, rest, ok := strings.Cut(w, "://")
		if !ok {
			continue
		}
		host, _, _ := strings.Cut(rest, "/")
		if strings.Contains(host, "@") {
			return true
		}
	}
	return false
}

// hasValue says whether the right side of an assignment gives a value: X=, X=” and X= # note give nothing. What cannot be told
// (X=$EMPTY) counts as set.
func hasValue(raw string) bool {
	v, ok := plainValue(raw)
	return !ok || v != ""
}
