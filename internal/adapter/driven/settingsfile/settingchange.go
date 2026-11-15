package settingsfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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

// lines is a setting whose value is text over several lines (commands).
func lines(name string, danger bool) spec {
	return spec{SettingInfo: settings.SettingInfo{Name: name, Type: "lines", Danger: danger}}
}

func flag(name string) spec { return enum(name, false, false, "true", "false") }

// catalog is what a change may name. VIPER_POST_CLONE (commands that run on the server) is on it as a danger: it asks for the word.
func catalog() []spec {
	return []spec{
		enum("PROVIDER", false, true, model.ProviderNames()...), enum("DNS_PROVIDER", false, true, "cloudflare", "none"), text("DNS_DOMAIN", false, true),
		text("SERVER_PREFIX", false, true), text("SERVER_TYPE", false, false), text("SERVER_LOCATION", false, true), text("SERVER_IMAGE", false, false),
		text("COMPOSE_IMAGE", false, false), text("K8S_IMAGE", false, false), enum("K8S_DOCKER", true, false, "none", "dind", "remote"),
		enum("COMPOSE_DOCKER", true, false, "none", "dind", "remote"), text("WORKSPACE_DIR", true, false), text("REPO_URL", false, false), text("REPO_BRANCH", false, false),
		text("SSH_ALLOWED_CIDRS", true, false), text("GROUP_MACHINES", false, true),
		flag("NATS_ENABLED"), text("NATS_GROUP", false, false), number("NATS_PORT", 1024, 65535), flag("NATS_MCP"), enum("NATS_MCP_AUTOALLOW", true, false, "off", "read", "all"),
		text("NATS_MCP_PUSH_FROM", false, false), text("NATS_MCP_TOPICS", false, false), flag("NATS_OBJECTS"), number("NATS_OBJECTS_MAX", 16, 102400),
		number("NATS_OBJECTS_FILE_MAX", 1, 64), number("NATS_OBJECTS_TTL", 1, 365),
		enum("VIPER_HOMEBREW", false, false, "0", "1"), enum("VIPER_BREWFILE", false, false, "0", "1"), text("VIPER_BREW_TAPS", false, false), text("VIPER_BREW_TRUST", false, false),
		text("VIPER_BREW_PACKAGES", false, false), enum("VIPER_CODE_SERVER", false, false, "0", "1"), text("VIPER_SWAP", false, false), text("VIPER_CLAUDE_MODEL", false, false),
		number("VIPER_CLAUDE_MAX_CONTEXT_TOKENS", 1000, 10_000_000),
		lines("VIPER_POST_CLONE", true), enum("VIPER_POST_CLONE_ON_ERROR", false, false, "retry", "ignore", "abort"),
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
	texts := helpTexts()
	for _, s := range catalog() {
		s.Help = map[string]string{"en": texts[s.Name][0], "de": texts[s.Name][1]}
		out = append(out, s.SettingInfo)
	}
	return out
}

const (
	maxSettingText = 1024
	maxLinesText   = 8 << 10 // commands over several lines
)

// checkValue is the value of a setting by its kind: the limits of the kind, one line, and nothing a URL with credentials.
func (s spec) checkValue(v string) error {
	if s.Type == "lines" {
		return checkLines(v)
	}
	switch {
	case v == "":
		return errors.New("empty: take the setting out instead")
	case len(v) > maxSettingText:
		return fmt.Errorf("longer than %d characters", maxSettingText)
	case strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || (r >= 0x7f && r <= 0x9f) }) || !validUTF8(v):
		return errors.New("one line of text, no control character")
	case userinfoURL(v):
		return errors.New("a URL with credentials (use VIPER_GH_TOKEN or GIT_DEPLOY_KEY_FILE)")
	}
	if err := s.checkKind(v); err != nil {
		return err
	}
	return s.checkByName(v)
}

// checkKind is the limits of the kind of value: a choice, or a number from .. to ..
func (s spec) checkKind(v string) error {
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
	return nil
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
	case "SSH_ALLOWED_CIDRS":
		for _, n := range strings.Split(strings.TrimSuffix(v, ","), ",") {
			if !cidr4.MatchString(n) && !cidr6.MatchString(n) {
				return fmt.Errorf("%q is not a network (a.b.c.d/n or an IPv6 network)", n)
			}
		}
	case "VIPER_SWAP":
		return checkSwap(v)
	}
	return nil
}

var (
	cidr4 = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`)
	cidr6 = regexp.MustCompile(`^[0-9a-fA-F:]+/[0-9]{1,3}$`)
)

// checkSwap is VIPER_SWAP as preflight reads it: 0, a factor of the RAM from 0.1 to 4, or a size (4G, 512M).
func checkSwap(v string) error {
	if v == "0" || regexp.MustCompile(`^[1-9][0-9]*[MG]$`).MatchString(v) {
		return nil
	}
	if regexp.MustCompile(`^[0-9]*\.?[0-9]+$`).MatchString(v) {
		if x, err := strconv.ParseFloat(v, 64); err == nil && x >= 0.1 && x <= 4 {
			return nil
		}
	}
	return errors.New("0, a factor of the RAM from 0.1 to 4, or a size like 4G or 512M")
}

// checkLines is a value over several lines: text with line breaks (\n) and tabs, nothing else that is a control character, and \r
// never (the files have Unix line ends).
func checkLines(v string) error {
	switch {
	case strings.TrimSpace(v) == "":
		return errors.New("empty: take the setting out instead")
	case len(v) > maxLinesText:
		return fmt.Errorf("longer than %d characters", maxLinesText)
	case !validUTF8(v) || strings.ContainsFunc(v, func(r rune) bool { return (r < ' ' && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f) }):
		return errors.New("text with line breaks, no other control character")
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

// oldValue is the value the file has for a name now ("" if none, or if the file takes it from a variable or a ~: what the page
// would say would not be what the shell reads).
func (pl credPlan) oldValue(name string) string {
	as, err := Assignments(pl.before)
	if err != nil {
		return ""
	}
	a, found, _ := only(as, name)
	if !found || expands(pl.before[a.Start:a.End]) {
		return ""
	}
	return a.Value
}

// expands says whether the shell would expand something in a statement (a $, a backtick or a ~ outside single quotes): then the
// value the page knows is not the one that is meant.
func expands(raw string) bool {
	single := false
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '\'':
			single = !single
		case single:
		case c == '\\':
			i++
		case c == '$' || c == '`' || (c == '~' && (i == 0 || raw[i-1] == '=' || raw[i-1] == ':')):
			return true
		}
	}
	return false
}

// indent puts a value over several lines under its name.
func indent(v string) string { return strings.ReplaceAll(v, "\n", "\n      ") }

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
		return "~ " + name + ": " + indent(shown(old)) + " -> " + indent(value)
	}
	return "+ " + name + " = " + indent(value)
}

// machinesOf are the machines whose settings the file gives: the machines of .env.local (those that have a file and those a group
// names), the machines of a group (those it names now and those the change makes it name), the machine itself.
func machinesOf(dir string, ch settings.Change, env settings.Env) []string {
	set := map[string]bool{}
	add := func(ms ...string) {
		for _, m := range ms {
			set[m] = true
		}
	}
	switch ch.Target {
	case "machine":
		add(ch.Name)
	case "group":
		add(groupMembers(dir, ch.Name, env)...)
		if v, ok := ch.Fields["GROUP_MACHINES"]; ok {
			ms, _ := parseGroupMachines(v)
			add(ms...)
		}
	default:
		add(machineFiles(dir, env)...)
		for _, g := range groupFiles(dir, env) {
			add(groupMembers(dir, g, env)...)
		}
	}
	return slices.Sorted(func(yield func(string) bool) {
		for m := range set {
			if !yield(m) {
				return
			}
		}
	})
}

func groupMembers(dir, name string, env settings.Env) []string {
	if g, err := LoadGroup(dir, name, env); err == nil {
		return g.Machines
	}
	return nil
}

// machineFiles are the machines that have a file (not the examples).
func machineFiles(dir string, env settings.Env) []string {
	var out []string
	ents, _ := readDir(env, filepath.Join(dir, "machines"))
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".env.local"); ok && !e.IsDir() && n != "" && !strings.HasPrefix(n, ".") && !strings.HasPrefix(n, "example") {
			out = append(out, n)
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// withServer are the machines of the list that have a mark of this computer: a server that viper down has to find. A machine
// that is named in several groups is looked at in each of them; one that cannot be read at all counts as having a server if a
// mark with its name is there (it may, and a wrong guess only keeps a setting where it is).
func withServer(dir string, env settings.Env, machines []string) []string {
	var out []string
	for _, m := range machines {
		if hasServer(dir, env, m) {
			out = append(out, m)
		}
	}
	return out
}

func marked(c *model.Machine) bool {
	return exists(c.IDState) || exists(c.ReadyState) || exists(c.GroupState)
}

func hasServer(dir string, env settings.Env, m string) bool {
	if c, err := load(dir, m, env, false); err == nil {
		return marked(c)
	}
	for _, g := range groupFiles(dir, env) {
		e := env
		e.Group = g
		if c, err := load(dir, m, e, false); err == nil && marked(c) {
			return true
		}
	}
	for _, kind := range []string{"id", "ready", "group"} {
		if hits, _ := filepath.Glob(filepath.Join(model.StateDir(env.Home), "servers", "*", kind+"-*-"+m)); len(hits) > 0 {
			return true
		}
	}
	return false
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
	case s.Type == "lines":
		return s.Name + ": shell commands that run on the server after the clone, with everything the user there may do"
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

// ReadSetting gives the value of a setting of a file that exists, for the settings that are text over several lines (the page of
// the settings cannot show them: it reads a line at a time). Nothing else is given out here.
func (Files) ReadSetting(dir, target, name, setting string, _ settings.Env) (string, error) {
	if sp, ok := specOf(setting); !ok || sp.Type != "lines" {
		return "", errors.New("this page gives out only the commands of a machine")
	}
	rel, err := targetRel(settings.Change{Target: target, Name: name})
	if err != nil {
		return "", err
	}
	if err := plainFile(dir, rel); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "", err
	}
	as, err := Assignments(string(b))
	if err != nil {
		return "", err
	}
	a, found, err := only(as, setting)
	if err != nil || !found || expands(string(b)[a.Start:a.End]) {
		return "", errors.New(setting + " is not set here, or the file takes it from a variable")
	}
	return a.Value, nil
}
