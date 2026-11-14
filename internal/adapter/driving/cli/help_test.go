package cli_test

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
)

// Every command has a help, and its options are exactly the options the command takes: a new option that nobody described, or a
// description of an option that is gone, fails here.
func TestEveryCommandHasAHelpThatDescribesExactlyItsOptions(t *testing.T) {
	for _, name := range cli.CommandNames() {
		text := cli.CommandHelp(name)
		if !strings.HasPrefix(text, "viper "+name+": ") || !strings.Contains(text, "Usage: viper "+name) || !strings.Contains(text, "--dir DIR") {
			t.Errorf("%s:\n%s", name, text)
		}
		got, want := append([]string(nil), cli.HelpOptionNames(name)...), append([]string(nil), cli.CommandFlags(name)...)
		sort.Strings(got)
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: the help describes %v, the command takes %v", name, got, want)
		}
		for _, line := range strings.Split(text, "\n") {
			if len(line) > 100 && !strings.Contains(line, "https://") && !strings.HasPrefix(line, "Usage:") { // an address and a usage line may be long
				t.Errorf("%s: a line of %d characters: %s", name, len(line), line)
			}
		}
	}
}

func TestEverySubcommandOfNatsHasAHelpWithOptionsThatExist(t *testing.T) {
	nats := map[string]bool{}
	for _, o := range cli.CommandFlags("nats") {
		nats[o] = true
	}
	subs := cli.NatsSubNames()
	if len(subs) != 9 {
		t.Errorf("%v", subs)
	}
	for _, sub := range subs {
		text := cli.NatsSubHelp(sub)
		if !strings.HasPrefix(text, "viper nats "+sub+"\n") || !strings.Contains(text, "Usage: viper nats "+sub) {
			t.Errorf("%s:\n%s", sub, text)
		}
		for _, o := range cli.NatsSubOptionNames(sub) {
			if !nats[o] {
				t.Errorf("%s: the option --%s does not exist", sub, o)
			}
			if !strings.Contains(text, "--"+o) {
				t.Errorf("%s: --%s is not in the help", sub, o)
			}
		}
	}
}

func helpOutput(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	_, h := fakeTools(t)
	var out, errb bytes.Buffer
	h.Stdout, h.Stderr = &out, &errb
	code := cli.Main("v9.9.9", args, h)
	return out.String(), errb.String(), code
}

func TestTheHelpIsForTheCommandThatWasAskedAbout(t *testing.T) {
	overview, _, code := helpOutput(t, "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, name := range cli.CommandNames() {
		if !strings.Contains(overview, "  "+name+" ") {
			t.Errorf("the overview does not list %s", name)
		}
	}
	for _, args := range [][]string{{}, {"help"}, {"-h"}} {
		if out, _, code := helpOutput(t, args...); code != 0 || out != overview {
			t.Errorf("%v: exit %d, not the overview", args, code)
		}
	}
	check := cli.CommandHelp("check")
	for _, args := range [][]string{{"check", "--help"}, {"check", "-h"}, {"help", "check"}, {"check", "web", "--help"}} {
		if out, errs, code := helpOutput(t, args...); code != 0 || out != check || errs != "" {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	if !strings.Contains(check, "--parallel N") || !strings.Contains(check, "--in-group G") {
		t.Errorf("%s", check)
	}
	for _, args := range [][]string{{"nats", "send", "--help"}, {"help", "nats", "send"}, {"nats", "--help", "send"}} {
		if out, _, code := helpOutput(t, args...); code != 0 || out != cli.NatsSubHelp("send") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	// nats alone, and a word that is no subcommand: the help of nats
	for _, args := range [][]string{{"nats", "--help"}, {"nats", "nonsense", "--help"}} {
		if out, _, code := helpOutput(t, args...); code != 0 || out != cli.CommandHelp("nats") {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	// an unknown command is an error, not the overview
	if out, errs, code := helpOutput(t, "help", "nope"); code != 2 || out != "" || !strings.Contains(errs, `unknown command "nope"`) {
		t.Errorf("exit %d %q %q", code, out, errs)
	}
}
