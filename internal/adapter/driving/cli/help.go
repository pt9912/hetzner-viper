package cli

import (
	"fmt"
	"sort"
	"strings"
)

// The help of viper: viper help (the commands in groups, one line each), and viper <command> --help (or viper help <command>)
// for one command: what it does, its usage, every option with what it means, examples and where the manual has more. The texts
// are here, next to the table of commands; a test sees to it that every option of a command is described and that nothing is
// described that the command does not take.

// optionDoc describes an option: name without the dashes, the name of its value ("" for an option without a value), the text.
type optionDoc struct {
	name, arg, text string
}

// commandDoc is the help of a command.
type commandDoc struct {
	group    string // the heading of the overview
	summary  string // one line, in the overview
	about    string // what the command does, in paragraphs separated by an empty line
	options  []optionDoc
	examples []string
	manual   string // the chapter of the manual (German)
}

// The groups of the overview, in the order they are shown.
const (
	helpMachines = "Set up and use a machine"
	helpGroups   = "Machines of a group, messages"
	helpImages   = "Images, prices, helpers"
)

func optDir() optionDoc {
	return optionDoc{"dir", "DIR", "the directory with .env.local (and machines/, groups/); a relative SSH_KEY is relative to it (default: the current directory)"}
}

func optInGroup() optionDoc {
	return optionDoc{"in-group", "G", "use the settings of the group G for this machine (needed only when several groups name it, and it does not run yet; a machine that runs is used in the group it was started in)"}
}

func optGroup() optionDoc {
	return optionDoc{"group", "G", "do it for all machines of groups/G.env at the same time, instead of one machine"}
}

func optParallel() optionDoc {
	return optionDoc{"parallel", "N", "how many machines of a group at the same time (1 to 64, default 8)"}
}

// helps are the helps of the commands by name.
func helps() map[string]commandDoc {
	all := helpsMachines()
	for _, m := range []map[string]commandDoc{helpsGroups(), helpsImages()} {
		for k, v := range m {
			all[k] = v
		}
	}
	return all
}

// helpsMachines are the helps of the commands of one group of the overview.
func helpsMachines() map[string]commandDoc {
	return map[string]commandDoc{
		"setup": {
			group:   helpMachines,
			summary: "once: create .env.local, your login key and the local host CA",
			about:   "Creates what viper needs on this computer, once: your .env.local (from a template), the login key for the machines and the host CA that signs the host keys of the servers. What exists is kept. Afterwards fill in .env.local.",
			examples: []string{
				"viper setup",
			},
			manual: "Erstmalige Einrichtung",
		},
		"preflight": {
			group:   helpMachines,
			summary: "check settings, setup, provider, Cloudflare and GitHub before anything is made",
			about:   "Checks, read only, everything viper up needs: your settings, the setup, your cloud provider (token, server type, location, image), Cloudflare (if used) and GitHub. It makes nothing. viper up runs it first.",
			options: []optionDoc{
				{"repo", "URL", "the repository to check as well (default: REPO_URL of the settings)"},
				optGroup(), optInGroup(), optParallel(),
			},
			examples: []string{"viper preflight web", "viper preflight --group dev"},
			manual:   "Vorab prüfen (Preflight)",
		},
		"up": {
			group:   helpMachines,
			summary: "set up a machine (server, pod or container) and clone the repository",
			about:   "Creates the machine, sets up the DNS name and the work directory, installs the tools, logs in GitHub and Claude Code, clones the repository and opens VS Code. A machine that exists is used again. With --group all machines of a group are set up at the same time (the server of a NATS group first).",
			options: []optionDoc{
				{"repo", "URL", "the repository to clone (default: REPO_URL of the settings)"},
				{"no-code", "", "do not open VS Code"},
				optGroup(), optInGroup(), optParallel(),
			},
			examples: []string{"viper up web", "viper up web --repo git@github.com:me/app.git --no-code", "viper up --group dev"},
			manual:   "Einen Rechner für ein Repository einrichten",
		},
		"check": {
			group:    helpMachines,
			summary:  "check a running machine, read only",
			about:    "Looks at a running machine and changes nothing: DNS, access by name as another device would have it, the state of the server (cloud-init, Docker, Claude Code, Homebrew, disk), and, for a machine in a NATS group, the group, the MCP server and the object store. It ends with how many problems it found.",
			options:  []optionDoc{optGroup(), optInGroup(), optParallel()},
			examples: []string{"viper check web", "viper check --group dev"},
			manual:   "Einen laufenden Rechner prüfen",
		},
		"ssh": {
			group:   helpMachines,
			summary: "open a shell on the machine or run a command there",
			about:   "Connects with the SSH configuration of the machine. Without options it opens a shell. With --cmd it runs the command (and for a group, on every machine). With --tmux it attaches to the tmux session claude, the one the terminals of VS Code use.",
			options: []optionDoc{
				{"cmd", "COMMAND", "run this command instead of a shell (a value that starts with a dash needs the form --cmd=VALUE)"},
				{"tmux", "", "attach to the tmux session claude (made in the work directory if it is missing); needs a terminal, not with --cmd"},
				optGroup(), optInGroup(), optParallel(),
			},
			examples: []string{"viper ssh web", "viper ssh web --cmd 'git status'", "viper ssh web --tmux", "viper ssh --group dev --cmd uptime"},
			manual:   "Mit Claude Code arbeiten",
		},
		"vscode": {
			group:    helpMachines,
			summary:  "open VS Code (Remote-SSH) on the machine",
			about:    "Opens VS Code on the running machine in its work directory, as viper up does after the setup. Needs the command code. The terminals there attach to the tmux session claude.",
			options:  []optionDoc{optInGroup()},
			examples: []string{"viper vscode web"},
			manual:   "Einen Rechner für ein Repository einrichten",
		},
		"down": {
			group:   helpMachines,
			summary: "delete a machine (after a check for unsaved work)",
			about:   "Checks the git state of the machine and warns about work that was not saved, then deletes the server (or the pod or container) and the DNS record. With the last machine of a k3d cluster that viper up made, the cluster goes too, and the registry after a question or by option. Nothing viper did not make is deleted.",
			options: []optionDoc{
				{"force", "", "delete even if the git state cannot be checked (the machine cannot be reached); unsaved work is lost"},
				{"delete-registry", "", "also delete the k3d registry that viper made (with --keep-registry: an error)"},
				{"keep-registry", "", "keep the k3d registry that viper made (it is yours from then on)"},
				optGroup(), optInGroup(), optParallel(),
			},
			examples: []string{"viper down web", "viper down --group dev --delete-registry"},
			manual:   "Einen Rechner beenden",
		},
		"dns": {
			group:    helpMachines,
			summary:  "print the DNS name of the machine",
			about:    "Prints <SERVER_PREFIX>-<machine>.<DNS_DOMAIN> without asking the provider or Cloudflare. With DNS_PROVIDER=none there is no name: it prints the address that was stored last.",
			options:  []optionDoc{optInGroup()},
			examples: []string{"viper dns web"},
			manual:   "Wie ein Rechner benannt wird",
		},
		"log": {
			group:   helpMachines,
			summary: "show the local logs",
			about:   "Shows what viper did on this computer: the events of all machines, of one machine, or with --full its complete output.",
			options: []optionDoc{
				{"full", "", "the complete output of the machine (needs a machine)"},
				optInGroup(),
			},
			examples: []string{"viper log", "viper log web --full"},
			manual:   "Protokolle ansehen",
		},
	}
}

// helpsGroups are the helps of the commands of one group of the overview.
func helpsGroups() map[string]commandDoc {
	return map[string]commandDoc{
		"nats": {
			group:    helpGroups,
			summary:  "talk to a NATS group: peers, send, request, listen, files",
			about:    "Speaks to the machines of a NATS group from this computer: who is there, which topics exist, messages, requests, rounds to a topic, and images, PDFs and ZIP files. Without a key of your own it runs as a machine of the group (over ssh), with one (viper nats init) as the operator. The subcommands:\n\n  peers GROUP      who said that it is there\n  topics GROUP     the topics of the settings, then the ones the running machines receive\n  listen GROUP     show what is sent (until Ctrl-C)\n  send GROUP TEXT --to MACHINE\n  broadcast GROUP TEXT --topic T\n  request GROUP TEXT --to MACHINE   wait for the answer\n  fetch GROUP ID --sha256 SUM       get a file (as the operator)\n  init GROUP       make your own key and register it (the operator)\n  revoke GROUP     remove it again\n\nviper nats <subcommand> --help describes one of them.",
			options:  natsOptionDocs(),
			examples: []string{"viper nats peers dev", "viper nats send dev \"push, please\" --to beta", "viper nats send dev \"look\" --to beta --pdf report.pdf"},
			manual:   "Nachrichten an eine NATS-Gruppe senden und mitlesen",
		},
	}
}

// helpsImages are the helps of the commands of one group of the overview.
func helpsImages() map[string]commandDoc {
	return map[string]commandDoc{
		"image": {
			group:   helpImages,
			summary: "build the image of the machines (PROVIDER=compose or kubernetes)",
			about:   "viper image build builds the image of the machines with Docker, for PROVIDER=compose and PROVIDER=kubernetes. For k3d it imports it into the cluster. The output names the image.",
			options: []optionDoc{
				{"tag", "NAME:TAG", "the name of the image (default: viper-machine:<version>)"},
				{"k3d", "CLUSTER", "import the image into this k3d cluster as well"},
			},
			examples: []string{"viper image build", "viper image build --k3d viper"},
			manual:   "Lokal arbeiten, ohne Cloud-Konto",
		},
		"find": {
			group:   helpImages,
			summary: "find the cheapest server types with at least this much CPU, RAM and disk",
			about:   "Looks through all providers for server types with at least the CPUs, memory and disk you ask for, and shows the cheapest with their price. Read only; needs no machine. At least one of --cpu, --ram and --disk is needed.",
			options: []optionDoc{
				{"cpu", "N", "at least this many CPUs"},
				{"ram", "GB", "at least this much memory"},
				{"disk", "GB", "at least this much disk"},
				{"arch", "x86|arm", "only this processor architecture (default: both)"},
				{"provider", "P", "only this provider: hetzner, aws, azure or gcp"},
				{"location", "L", "only this location, e.g. fsn1 (Hetzner), eu-central-1 (AWS); needs --provider"},
				{"vat", "PERCENT", "the VAT for all prices (--vat 0: net prices); default: the rate of your Hetzner project, else none"},
			},
			examples: []string{"viper find --cpu 4 --ram 16 --disk 80", "viper find --cpu 2 --ram 4 --provider hetzner --vat 19"},
			manual:   "Passenden Server-Typ suchen",
		},
		"mcp-binary": {
			group:    helpImages,
			summary:  "write the built-in MCP server (viper-nats-mcp) to stdout",
			about:    "Writes the MCP server that is built into viper, for one architecture, to standard output (redirect it into a file). To look at it, to check it, or as NATS_MCP_BINARY.",
			options:  []optionDoc{{"arch", "amd64|arm64", "the architecture of the machine the server is for"}},
			examples: []string{"viper mcp-binary --arch arm64 > viper-nats-mcp"},
			manual:   "Claude Code mit den anderen Rechnern der Gruppe sprechen lassen",
		},
		"k8s-proxy": {
			group:   helpImages,
			summary: "(used by the SSH configuration of a pod)",
			about:   "The ProxyCommand in the SSH configuration of a machine in a Kubernetes cluster: it connects ssh to the pod. You do not run it yourself.",
			options: []optionDoc{optInGroup()},
		},
		"docker-proxy": {
			group:   helpImages,
			summary: "(used by the SSH configuration of a container)",
			about:   "The ProxyCommand in the SSH configuration of a machine on a Docker host (PROVIDER=compose): it connects ssh to the container with docker exec. You do not run it yourself.",
			options: []optionDoc{optInGroup()},
		},
		"version": {
			group:   helpImages,
			summary: "print the version",
			about:   "Prints the version of viper.",
		},
	}
}

// natsOptionDocs are the options of viper nats; which subcommand takes which is in natsSubs.
func natsOptionDocs() []optionDoc {
	return []optionDoc{
		{"to", "MACHINE", "send, request: the machine that gets it"},
		{"topic", "T[,T...]", "broadcast: the topic; listen: only these topics (more than once or a list)"},
		{"from", "MACHINE", "speak as this machine (default: the operator if you have a key, else the first machine of the group that can be reached)"},
		{"file", "FILE", "instead of the text: the text in this file (- is standard input); at most 16 KiB"},
		{"pdf", "FILE", "send, broadcast, request: a PDF file with the message (NATS_OBJECTS)"},
		{"image", "FILE", "the same for an image (PNG, JPEG, GIF or WebP)"},
		{"zip", "FILE", "the same for a ZIP file (looked at, never unpacked)"},
		{"sha256", "SUM", "fetch: the checksum of the file, from the line of viper nats listen"},
		{"out", "DIR", "fetch: the folder to put the file in (default: the current one)"},
		{"timeout", "S", "request: how long to wait for the answer (1 to 120, default 30); peers, topics: how long to listen"},
		{"query", "", "peers, topics: ask the others to say that they are there (8 seconds instead of 35)"},
		{"all", "", "listen: also the messages between the other machines"},
		{"json", "", "one JSON line for every event"},
		{"yes", "", "send although the sender is in NATS_MCP_PUSH_FROM of a receiver (its text is then pushed into a session as if the sender had written it)"},
	}
}

// natsSub is the help of a subcommand of viper nats.
type natsSub struct {
	usage    string
	about    string
	options  []string // the names of the options it takes, from natsOptionDocs
	examples []string
}

// natsSubs are the subcommands of viper nats by name.
func natsSubs() map[string]natsSub {
	common := []string{"from", "json"}
	files := []string{"file", "pdf", "image", "zip", "yes"}
	return map[string]natsSub{
		"peers":     {"viper nats peers GROUP [--query] [--timeout S]", "Lists the machines that said recently (within 90 seconds) that they are there. A machine says it only while a Claude Code session runs there.", append([]string{"query", "timeout"}, common...), []string{"viper nats peers dev --query"}},
		"topics":    {"viper nats topics GROUP [--query] [--timeout S]", "Prints first the topics the settings give each machine (NATS_MCP_TOPICS), then the ones that running machines said they receive, and each topic with its machines.", append([]string{"query", "timeout"}, common...), []string{"viper nats topics dev"}},
		"listen":    {"viper nats listen GROUP [--topic T[,T...]] [--all]", "Shows the messages of the group until you press Ctrl-C. A message with a file is shown with the line that tells how to fetch it.", append([]string{"topic", "all"}, common...), []string{"viper nats listen dev --topic build"}},
		"send":      {"viper nats send GROUP [TEXT|-] --to MACHINE [--file FILE] [--pdf|--image|--zip FILE]", "Sends a message to one machine. TEXT may be - (standard input) or --file. With --pdf, --image or --zip a file goes with it; the text may then be left out. The group needs NATS_OBJECTS for files.", append([]string{"to"}, append(files, common...)...), []string{"viper nats send dev \"push, please\" --to beta", "viper nats send dev --to beta --pdf report.pdf"}},
		"broadcast": {"viper nats broadcast GROUP [TEXT|-] --topic T [--file FILE] [--pdf|--image|--zip FILE]", "Sends a message to a topic: every machine that subscribed to it gets it. Nobody who is not there gets it later.", append([]string{"topic"}, append(files, common...)...), []string{"viper nats broadcast dev \"release at 3\" --topic news"}},
		"request":   {"viper nats request GROUP [TEXT|-] --to MACHINE [--timeout S] [--pdf|--image|--zip FILE]", "Asks a machine and prints its answer, or fails when none comes in time. The answer is text of another agent: untrusted.", append([]string{"to", "timeout"}, append(files, common...)...), []string{"viper nats request dev \"status?\" --to beta --timeout 60"}},
		"fetch":     {"viper nats fetch GROUP ID --sha256 SUM [--out DIR]", "Gets a file of the object store of the group onto this computer, as <ID>.<type> in the folder, and checks it against the checksum. Only with the key of the operator (viper nats init). Nothing is unpacked or opened.", []string{"sha256", "out", "json"}, []string{"viper nats fetch dev 0123456789abcdef0123456789abcdef --sha256 <sum> --out ./in"}},
		"init":      {"viper nats init GROUP", "Makes a key of your own on this computer and registers it at the NATS server of the group: from then on viper nats speaks as the operator (from this computer, through an SSH tunnel). The agents see it as a sender of its own.", nil, []string{"viper nats init dev"}},
		"revoke":    {"viper nats revoke GROUP", "Removes the operator from the server and deletes the key on this computer.", nil, []string{"viper nats revoke dev"}},
	}
}

// overviewText is viper help: the commands in groups.
func overviewText(version string) string {
	docs := helps()
	var b strings.Builder
	b.WriteString("viper " + version + ": set up and use machines in the cloud for Claude Code (needs the OpenSSH client)\n\n")
	b.WriteString("Usage: viper <command> [options]\n       viper <command> --help     describes one command, its options and examples\n")
	for _, g := range []string{helpMachines, helpGroups, helpImages} {
		var names []string
		for n, d := range docs {
			if d.group == g {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		b.WriteString("\n" + g + ":\n")
		for _, n := range names {
			fmt.Fprintf(&b, "  %-13s %s\n", n, docs[n].summary)
		}
	}
	b.WriteString(`
Most commands take a machine (viper up web) or all machines of a group (viper up --group dev).
Option for every command:
  --dir DIR   the directory with .env.local (and machines/); a relative SSH_KEY is relative to it
              (default: the current directory)

Values that start with a dash need the form --cmd=VALUE, for example viper ssh web --cmd="ls -la".

` + manualLines(version))
	return b.String()
}

// commandHelp is viper <command> --help.
func commandHelp(name, version string) string {
	d := helps()[name]
	var b strings.Builder
	fmt.Fprintf(&b, "viper %s: %s\n\n", name, d.summary)
	b.WriteString(wrap(d.about, 0) + "\n\n")
	b.WriteString(usageLine(name) + "\n")
	opts := append(append([]optionDoc{}, d.options...), optDir())
	b.WriteString("\nOptions:\n" + optionsText(opts))
	if len(d.examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range d.examples {
			b.WriteString("  " + e + "\n")
		}
	}
	b.WriteString("\n" + manualLine(d.manual, version))
	return b.String()
}

// natsSubHelp is viper nats <subcommand> --help.
func natsSubHelp(sub, version string) string {
	s := natsSubs()[sub]
	byName := map[string]optionDoc{}
	for _, o := range natsOptionDocs() {
		byName[o.name] = o
	}
	var opts []optionDoc
	for _, n := range s.options {
		opts = append(opts, byName[n])
	}
	opts = append(opts, optDir())
	var b strings.Builder
	fmt.Fprintf(&b, "viper nats %s\n\n%s\n\nUsage: %s\n\nOptions:\n%s", sub, wrap(s.about, 0), s.usage, optionsText(opts))
	if len(s.examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range s.examples {
			b.WriteString("  " + e + "\n")
		}
	}
	b.WriteString("\n" + manualLine(helps()["nats"].manual, version))
	return b.String()
}

// optionsText lists options: the option with its value, then the text, wrapped.
func optionsText(opts []optionDoc) string {
	var b strings.Builder
	for _, o := range opts {
		left := "--" + o.name
		if o.arg != "" {
			left += " " + o.arg
		}
		lines := strings.Split(wrap(o.text, 0), "\n")
		if len(left) > 22 {
			b.WriteString("  " + left + "\n")
			for _, l := range lines {
				b.WriteString(strings.Repeat(" ", 26) + l + "\n")
			}
			continue
		}
		fmt.Fprintf(&b, "  %-24s%s\n", left, lines[0])
		for _, l := range lines[1:] {
			b.WriteString(strings.Repeat(" ", 26) + l + "\n")
		}
	}
	return b.String()
}

// wrap breaks text into lines of at most 68 characters (paragraphs and the lines of a list stay as they are).
func wrap(text string, indent int) string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if strings.HasPrefix(para, "  ") || para == "" {
			out = append(out, para)
			continue
		}
		line := ""
		for _, w := range strings.Fields(para) {
			if line != "" && len(line)+1+len(w) > 68 {
				out = append(out, strings.Repeat(" ", indent)+line)
				line = w
				continue
			}
			if line != "" {
				line += " "
			}
			line += w
		}
		out = append(out, strings.Repeat(" ", indent)+line)
	}
	return strings.Join(out, "\n")
}

// manualLine names the chapter of the manual (German) and where it is.
func manualLine(chapter, version string) string {
	if chapter == "" {
		return manualLines(version)
	}
	head := "Manual (German), section \"" + chapter + "\":\n"
	if !releaseVersion.MatchString(version) {
		return head + "  " + manualBase + "main" + manualPath + "\n"
	}
	return head + "  this version: " + manualBase + version + manualPath + "\n  current:      " + manualBase + "main" + manualPath + "\n"
}

// helpError asks for the help of a command (and of a subcommand of viper nats); name is "" for the overview.
type helpError struct{ name, sub string }

func (*helpError) Error() string { return "help" }

// Is makes a helpError an errHelp.
func (*helpError) Is(target error) bool { return target == errHelp }

// helpFor is the text a helpError asks for.
func helpFor(e *helpError, version string) string {
	switch {
	case e.name == "":
		return overviewText(version)
	case e.name == "nats" && natsSubs()[e.sub].usage != "":
		return natsSubHelp(e.sub, version)
	}
	return commandHelp(e.name, version)
}
