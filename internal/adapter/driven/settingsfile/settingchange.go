package settingsfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// Stage B (docs/plan/ui-bearbeiten.md): the settings of the files that exist, except credentials (stage C). Only settings on a
// list, each with a kind of value; the files are checked as a whole by loading every machine and group with the change.

type spec struct {
	settings.SettingInfo
	mark bool // not changed while a machine of the file has a server (viper down looks where the settings say)
}

func enum(name string, danger bool, mark bool, options ...string) spec {
	return spec{SettingInfo: settings.SettingInfo{Name: name, Type: "enum", Options: options, Danger: danger}, mark: mark}
}

func text(name string, danger, mark bool) spec {
	return spec{SettingInfo: settings.SettingInfo{Name: name, Type: "text", Danger: danger}, mark: mark}
}

func number(name string, lo, hi int) spec {
	return spec{SettingInfo: settings.SettingInfo{Name: name, Type: "int", Min: lo, Max: hi}}
}

func flag(name string) spec { return enum(name, false, false, "true", "false") }

// catalog is what a change may name. VIPER_POST_CLONE (commands that run on the server) is not on it: change it at the terminal.
func catalog() []spec {
	return []spec{
		enum("PROVIDER", false, true, model.ProviderNames()...), enum("DNS_PROVIDER", false, false, "cloudflare", "none"), text("DNS_DOMAIN", false, false),
		text("SERVER_PREFIX", false, true), text("SERVER_TYPE", false, false), text("SERVER_LOCATION", false, true), text("SERVER_IMAGE", false, false),
		text("COMPOSE_IMAGE", false, false), text("K8S_IMAGE", false, false), enum("K8S_DOCKER", true, false, "none", "dind", "remote"),
		enum("COMPOSE_DOCKER", true, false, "none", "dind", "remote"), text("WORKSPACE_DIR", true, false), text("REPO_URL", false, false), text("REPO_BRANCH", false, false),
		text("SSH_ALLOWED_CIDRS", true, false), text("GROUP_MACHINES", false, true),
		flag("NATS_ENABLED"), text("NATS_GROUP", false, false), number("NATS_PORT", 1024, 65535), flag("NATS_MCP"), enum("NATS_MCP_AUTOALLOW", true, false, "off", "read", "all"),
		text("NATS_MCP_PUSH_FROM", false, false), text("NATS_MCP_TOPICS", false, false), flag("NATS_OBJECTS"), number("NATS_OBJECTS_MAX", 16, 102400),
		number("NATS_OBJECTS_FILE_MAX", 1, 64), number("NATS_OBJECTS_TTL", 1, 365),
		enum("VIPER_HOMEBREW", false, false, "0", "1"), enum("VIPER_BREWFILE", false, false, "0", "1"), text("VIPER_BREW_TAPS", false, false), text("VIPER_BREW_TRUST", false, false),
		text("VIPER_BREW_PACKAGES", false, false), text("VIPER_CODE_SERVER", false, false), text("VIPER_SWAP", false, false), text("VIPER_CLAUDE_MODEL", false, false),
		number("VIPER_CLAUDE_MAX_CONTEXT_TOKENS", 1000, 10_000_000),
	}
}

func specOf(name string) (spec, bool) {
	for _, s := range catalog() {
		if s.Name == name {
			return s, true
		}
	}
	return spec{}, false
}

// Catalog lists the settings a change may name.
func (Files) Catalog() []settings.SettingInfo {
	out := make([]settings.SettingInfo, 0, len(catalog()))
	for _, s := range catalog() {
		s.Help = map[string]string{"en": help[s.Name][0], "de": help[s.Name][1]}
		out = append(out, s.SettingInfo)
	}
	return out
}

const maxSettingText = 1024

// checkValue is the value of a setting by its kind: the limits of the kind, one line, and nothing a URL with credentials.
func (s spec) checkValue(v string) error {
	switch {
	case len(v) > maxSettingText:
		return fmt.Errorf("longer than %d characters", maxSettingText)
	case strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || (r >= 0x7f && r <= 0x9f) }) || !validUTF8(v):
		return errors.New("one line of text, no control character")
	case userinfoURL(v):
		return errors.New("a URL with credentials (use VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE)")
	}
	switch s.Type {
	case "enum":
		if !slices.Contains(s.Options, v) {
			return fmt.Errorf("one of %s", strings.Join(s.Options, ", "))
		}
	case "int":
		n := 0
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil || fmt.Sprint(n) != v || n < s.Min || n > s.Max {
			return fmt.Errorf("a number from %d to %d", s.Min, s.Max)
		}
	}
	return s.checkByName(v)
}

// checkByName is what a setting asks besides its kind.
func (s spec) checkByName(v string) error {
	switch s.Name {
	case "SERVER_PREFIX":
		if !nameRe.MatchString(v) {
			return errors.New("lowercase letters, digits and hyphens")
		}
	case "REPO_URL":
		if strings.HasPrefix(v, "-") {
			return errors.New("must not start with '-'")
		}
	case "GROUP_MACHINES":
		if _, err := parseGroupMachines(v); err != nil {
			return err
		}
	}
	return nil
}

func validUTF8(v string) bool { return strings.ToValidUTF8(v, "�") == v }

func checkSettings(ch settings.Change) (problems []string) {
	check := func(n string) (spec, bool) {
		s, ok := specOf(n)
		if !ok {
			problems = append(problems, n+": not a setting this page changes (credentials: the page for credentials; VIPER_POST_CLONE: the terminal)")
		}
		return s, ok
	}
	for n, v := range ch.Fields {
		if s, ok := check(n); ok {
			if err := s.checkValue(v); err != nil {
				problems = append(problems, n+": "+err.Error())
			}
		}
		if slices.Contains(ch.Remove, n) {
			problems = append(problems, n+": set and removed at once")
		}
	}
	for _, n := range ch.Remove {
		check(n)
	}
	if len(ch.Fields)+len(ch.Remove) == 0 {
		problems = append(problems, "nothing to change")
	}
	return problems
}

// oldValue is the value the file has for a name now ("" if none).
func (pl credPlan) oldValue(name string) string {
	as, err := Assignments(pl.before)
	if err != nil {
		return ""
	}
	if a, found, _ := only(as, name); found {
		return a.Value
	}
	return ""
}

// describe is the line of the preview for one setting: for a credential never a value, for a setting the old and the new one.
func describe(kind, name, value, old string, existed bool) string {
	if kind != "setting" {
		if existed {
			return "~ " + name + ": replaced"
		}
		return "+ " + name + ": set"
	}
	shown := func(v string) string {
		if v == "" || userinfoURL(v) {
			return "(set)"
		}
		return v
	}
	if existed {
		return "~ " + name + ": " + shown(old) + " -> " + value
	}
	return "+ " + name + " = " + value
}

// machinesOf are the machines whose settings the file gives: all for .env.local, the members of a group, the machine itself.
func machinesOf(dir string, ch settings.Change, env settings.Env) []string {
	switch ch.Target {
	case "machine":
		return []string{ch.Name}
	case "group":
		if g, err := LoadGroup(dir, ch.Name, env); err == nil {
			return g.Machines
		}
		return nil
	}
	var out []string
	ents, _ := readDir(env, filepath.Join(dir, "machines"))
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".env.local"); ok && !e.IsDir() && n != "" && !strings.HasPrefix(n, ".") && !strings.HasPrefix(n, "example") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// withServer are the machines of the list that have a mark of this computer: a server that viper down has to find.
func withServer(dir string, env settings.Env, machines []string) []string {
	var out []string
	for _, m := range machines {
		c, err := load(dir, m, env, false)
		if err == nil && (exists(c.IDState) || exists(c.ReadyState) || exists(c.GroupState)) {
			out = append(out, m)
		}
	}
	return out
}

// confirmWord is what a person types to confirm a dangerous change: the name of the file's machine or group, or the file.
func confirmWord(ch settings.Change) string {
	if ch.Target == "base" {
		return ".env.local"
	}
	return ch.Name
}

// dangerous says why a value of a setting needs a confirmation ("" if it does not).
func dangerous(s spec, value string, removed bool) string {
	switch {
	case !s.Danger:
		return ""
	case removed:
		return s.Name + ": taking it out gives the setting its default or the environment's value"
	case s.Name == "K8S_DOCKER" || s.Name == "COMPOSE_DOCKER":
		if value == "none" {
			return ""
		}
		return s.Name + "=" + value + ": the machine gets a Docker that is privileged or on another host"
	case s.Name == "NATS_MCP_AUTOALLOW":
		if value == "all" {
			return "NATS_MCP_AUTOALLOW=all: every message may use the tools without asking"
		}
		return ""
	case s.Name == "SSH_ALLOWED_CIDRS":
		return "SSH_ALLOWED_CIDRS: who may connect to the server (0.0.0.0/0 is everyone)"
	case s.Name == "WORKSPACE_DIR":
		return "WORKSPACE_DIR: where on the server the repository and the work are"
	}
	return s.Name + ": a setting that changes what the machine does"
}

// checkSettingChange adds what only a change of settings needs to the preview: the settings that are not changed while a server
// is there, the confirmation of dangerous ones, and what the change reaches.
func checkSettingChange(dir string, ch settings.Change, env settings.Env, p *settings.Preview) {
	machines := machinesOf(dir, ch, env)
	running := withServer(dir, env, machines)
	names := make([]string, 0, len(ch.Fields)+len(ch.Remove))
	for n := range ch.Fields {
		names = append(names, n)
	}
	names = append(names, ch.Remove...)
	sort.Strings(names)
	for _, n := range names {
		s, _ := specOf(n)
		if s.mark && len(running) > 0 {
			p.Problems = append(p.Problems, fmt.Sprintf("%s: not changed while %s has a server (viper down first: it looks where the settings say)", n, strings.Join(running, ", ")))
		}
		_, isSet := ch.Fields[n]
		if why := dangerous(s, ch.Fields[n], !isSet); why != "" {
			p.Confirm = append(p.Confirm, why)
		}
	}
	if len(p.Confirm) > 0 {
		p.ConfirmWord = confirmWord(ch)
	}
	switch ch.Target {
	case "base":
		p.Notes = append(p.Notes, fmt.Sprintf("this file gives settings to %d machine(s)", len(machines)))
	case "group":
		p.Notes = append(p.Notes, fmt.Sprintf("this file gives settings to the %d machine(s) of the group", len(machines)))
	}
	if len(running) > 0 && len(p.Problems) == 0 {
		p.Notes = append(p.Notes, strings.Join(running, ", ")+": has a server; the change applies from the next viper up")
	}
}
