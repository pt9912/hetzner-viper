package settingsfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// groupDir is the directory of the group files, next to machines/ (docs/plan/gruppen.md).
const groupDir = "groups"

// membership is what the group files say about one machine.
type membership struct {
	name     string // the group, "" if the machine is in none
	file     string // its file
	warnings []string
}

// groupFiles are the names of the groups that have a file in dir/groups, sorted. A missing directory
// is no group at all.
func groupFiles(dir string) []string {
	ents, err := os.ReadDir(filepath.Join(dir, groupDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".env"); ok && !e.IsDir() && n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// GroupFileOf is the path of the file of the group name.
func GroupFileOf(dir, name string) string { return filepath.Join(dir, groupDir, name+".env") }

// parseGroupMachines splits GROUP_MACHINES: names separated by commas, blanks removed, no duplicates, not empty.
func parseGroupMachines(v string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, m := range strings.Split(v, ",") {
		if m = strings.TrimSpace(m); m == "" {
			continue
		}
		if !nameRe.MatchString(m) {
			return nil, fmt.Errorf("GROUP_MACHINES: %q is no machine name (lowercase letters, digits, hyphens)", m)
		}
		if seen[m] {
			return nil, fmt.Errorf("GROUP_MACHINES: %s twice", m)
		}
		seen[m] = true
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, errors.New("GROUP_MACHINES: no machines")
	}
	return out, nil
}

// LoadGroup reads the file of the group name: the machines it names (in the order of the file) and
// its settings, checked.
func LoadGroup(dir, name string, env settings.Env) (*model.Group, error) {
	if err := model.CheckNATSName("group name", name); err != nil {
		return nil, err
	}
	file := GroupFileOf(dir, name)
	vars := map[string]string{}
	if err := sourceFile(vars, file, env); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("No group %s: there is no %s", name, filepath.Join(groupDir, name+".env"))
		}
		return nil, err
	}
	machines, err := parseGroupMachines(vars["GROUP_MACHINES"])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(groupDir, name+".env"), err)
	}
	if g := strings.TrimSpace(vars["NATS_GROUP"]); g != "" && g != name {
		return nil, fmt.Errorf("%s: NATS_GROUP is %s, but the group is %s (the name of the file)", filepath.Join(groupDir, name+".env"), g, name)
	}
	return &model.Group{Name: name, File: file, Machines: machines}, nil
}

// memberOf finds the group whose file names the machine. A file that cannot be read or checked is a
// warning, unless its text names the machine: then the machine would silently lose its group, and that
// is an error. A machine in two groups is an error.
func memberOf(dir, machine string, env settings.Env) (membership, error) {
	var m membership
	var files []string
	for _, n := range groupFiles(dir) {
		g, err := LoadGroup(dir, n, env)
		if err != nil {
			if namesMachine(GroupFileOf(dir, n), machine) {
				return m, err
			}
			m.warnings = append(m.warnings, fmt.Sprintf("%s is ignored: %v", filepath.Join(groupDir, n+".env"), err))
			continue
		}
		for _, x := range g.Machines {
			if x == machine {
				files = append(files, n)
			}
		}
	}
	switch len(files) {
	case 0:
	case 1:
		m.name, m.file = files[0], GroupFileOf(dir, files[0])
	default:
		return m, fmt.Errorf("The machine %s is named in the groups %s: a machine belongs to one group", machine, strings.Join(files, ", "))
	}
	return m, nil
}

var groupMachinesLine = regexp.MustCompile(`(?m)^\s*(?:export\s+)?GROUP_MACHINES=(.*)$`)

// namesMachine looks at the raw text of a group file for a line GROUP_MACHINES= that names the machine.
func namesMachine(file, machine string) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	for _, m := range groupMachinesLine.FindAllSubmatch(b, -1) {
		for _, f := range strings.FieldsFunc(string(m[1]), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '"' || r == '\'' }) {
			if f == machine {
				return true
			}
		}
	}
	return false
}

// applyGroup puts the settings of the group file between .env.local and the file of the machine,
// and NATS_GROUP as the name of the group.
func applyGroup(vars map[string]string, m membership, env settings.Env) error {
	if m.file == "" {
		return nil
	}
	if err := sourceFile(vars, m.file, env); err != nil {
		return err
	}
	vars["NATS_GROUP"] = m.name
	return nil
}

// checkMachineGroup: the file of a machine must not name another NATS group than its group file.
func checkMachineGroup(vars map[string]string, m membership, machine string) error {
	if m.file != "" && vars["NATS_GROUP"] != m.name {
		return fmt.Errorf("machines/%s.env.local: NATS_GROUP is %s, but the machine is in the group %s (groups/%s.env)", machine, vars["NATS_GROUP"], m.name, m.name)
	}
	return nil
}
