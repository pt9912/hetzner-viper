package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"sync"
)

const maxText = 16 << 10 // what a message may hold (natsmcp.DefaultMaxText)

var (
	topicRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.,-]{0,127}$`)
	idRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	shaRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// runFiles are the files a NATS run has on this computer, in a folder that only this user reaches: the text of a message, the
// file to send (taken from the uploads) and the folder a fetched file is put in.
type runFiles struct {
	dir    string
	upload string // the file that went out; it is deleted with the run
	keep   bool   // a fetch: the file stays for the page to take, until the run is forgotten
	mu     sync.Mutex
}

// finish is called when the run has ended.
func (f *runFiles) finish() {
	if f == nil {
		return
	}
	if f.keep {
		f.removeInput()
		return
	}
	f.remove()
}

func (f *runFiles) remove() {
	if f == nil {
		return
	}
	f.removeInput()
	_ = os.RemoveAll(f.dir)
}

// removeInput deletes what was made to be sent; the folder of a fetch stays until the server ends.
func (f *runFiles) removeInput() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upload != "" {
		_ = os.RemoveAll(filepath.Dir(f.upload))
		f.upload = ""
	}
	_ = os.Remove(filepath.Join(f.dir, "text"))
}

func findNATS(ov Overview, name string) (NATSGroup, bool) {
	for _, g := range ov.NATS {
		if g.Name == name {
			return g, true
		}
	}
	return NATSGroup{}, false
}

// natsArgv is the command line of viper nats for a request, or the reason there is none. Everything that is not a fixed word
// is checked: the group and the machines are in the overview, the rest has a pattern or a limit; options go in the form
// --name=value, so that no value is taken for an option.
func (s *Server) natsArgv(req runRequest, ov Overview) (argv []string, files *runFiles, err error) {
	g, ok := findNATS(ov, req.NATS)
	if !ok {
		return nil, nil, fmt.Errorf("no such NATS group")
	}
	if !slices.Contains([]string{"peers", "topics", "listen", "send", "broadcast", "request", "fetch"}, req.Sub) {
		return nil, nil, fmt.Errorf("no such subcommand")
	}
	for _, m := range []string{req.To, req.From} {
		if m != "" && !slices.Contains(g.Machines, m) {
			return nil, nil, fmt.Errorf("%s is not a machine of the group", m)
		}
	}
	argv = []string{"nats", req.Sub, g.Name}
	if err := checkNATSFields(req); err != nil {
		return nil, nil, err
	}
	if req.From != "" {
		argv = append(argv, "--from="+req.From)
	}
	if req.Timeout > 0 && (req.Sub == "peers" || req.Sub == "topics" || req.Sub == "request") {
		argv = append(argv, "--timeout="+strconv.Itoa(req.Timeout))
	}
	files, err = s.natsOptions(req, &argv)
	if err != nil {
		return nil, nil, err
	}
	return append(argv, "--json"), files, nil
}

// natsOptions adds the options and words of the subcommand, and makes the files of the run it needs.
func (s *Server) natsOptions(req runRequest, argv *[]string) (*runFiles, error) {
	switch req.Sub {
	case "peers", "topics":
		if req.Query {
			*argv = append(*argv, "--query")
		}
	case "listen":
		if req.Topic != "" {
			*argv = append(*argv, "--topic="+req.Topic)
		}
		if req.All {
			*argv = append(*argv, "--all")
		}
	case "send", "broadcast", "request":
		return s.message(req, argv)
	case "fetch":
		if !idRe.MatchString(req.ID) || !shaRe.MatchString(req.SHA256) {
			return nil, fmt.Errorf("fetch needs the id and the sha256 of the file (64 hex digits)")
		}
		files, err := newRunFiles()
		if err != nil {
			return nil, err
		}
		files.keep = true
		*argv = append(*argv, "--sha256="+req.SHA256, "--out="+filepath.Join(files.dir, "out"), req.ID) // an id does not begin with a dash (idRe)
		return files, nil
	}
	return nil, nil
}

// checkNATSFields refuses what does not belong to the subcommand or is out of bounds.
func checkNATSFields(req runRequest) error {
	if req.Topic != "" && !topicRe.MatchString(req.Topic) {
		return fmt.Errorf("the topic is not usable")
	}
	if req.Timeout < 0 || req.Timeout > 600 {
		return fmt.Errorf("the timeout is 1 to 600 seconds")
	}
	if len(req.Text) > maxText {
		return fmt.Errorf("the text has more than %d bytes", maxText)
	}
	switch req.Sub {
	case "send", "request":
		if req.To == "" {
			return fmt.Errorf("%s needs a machine", req.Sub)
		}
	case "broadcast":
		if req.Topic == "" {
			return fmt.Errorf("broadcast needs a topic")
		}
	}
	return nil
}

func newRunFiles() (*runFiles, error) {
	dir, err := os.MkdirTemp("", "viper-ui-run-")
	if err != nil {
		return nil, err
	}
	return &runFiles{dir: dir}, nil
}

// message adds the options of a message: the text goes by a file of this run (never in the command line), a file to send is the
// one that was handed over before.
func (s *Server) message(req runRequest, argv *[]string) (*runFiles, error) {
	if req.Text == "" && req.Upload == "" {
		return nil, fmt.Errorf("a message needs a text or a file")
	}
	files, err := newRunFiles()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*runFiles, error) { files.remove(); return nil, err }
	switch req.Sub {
	case "send", "request":
		*argv = append(*argv, "--to="+req.To)
	case "broadcast":
		*argv = append(*argv, "--topic="+req.Topic)
	}
	if req.Text != "" {
		if err := os.WriteFile(filepath.Join(files.dir, "text"), []byte(req.Text), 0o600); err != nil {
			return fail(err)
		}
		*argv = append(*argv, "--file="+filepath.Join(files.dir, "text"))
	}
	if req.Upload != "" {
		u, ok := s.uploads.take(req.Upload)
		if !ok {
			return fail(fmt.Errorf("no such file: hand it over again"))
		}
		files.upload = u.path
		*argv = append(*argv, "--"+u.kind+"="+u.path)
	}
	if req.Yes {
		*argv = append(*argv, "--yes")
	}
	return files, nil
}
