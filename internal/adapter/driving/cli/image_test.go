package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
)

// imageRun runs viper image build with a docker that looks at the build context it is given.
func imageRun(t *testing.T, version string, args ...string) (calls [][]string, out string, code int, missing []string) {
	t.Helper()
	_, h := fakeTools(t)
	var stdout, stderr bytes.Buffer
	h.Stdout, h.Stderr = &stdout, &stderr
	h.RunTool = func(name string, a ...string) error {
		calls = append(calls, append([]string{name}, a...))
		if name != "docker" {
			return nil
		}
		dockerfile := a[2] // docker build -f <dir>/image/Dockerfile -t <tag> <dir>
		ctx := a[len(a)-1]
		b, err := os.ReadFile(dockerfile)
		if err != nil {
			t.Fatalf("no Dockerfile in the context: %v", err)
		}
		// every file the Dockerfile copies must be in the context, or the build fails on a user's computer only
		for _, m := range regexp.MustCompile(`(?m)^COPY (?:--\S+ )*(\S+) `).FindAllStringSubmatch(string(b), -1) {
			if _, err := os.Stat(filepath.Join(ctx, m[1])); err != nil {
				missing = append(missing, m[1])
			}
		}
		return nil
	}
	code = cli.Main(version, append([]string{"image"}, args...), h)
	return calls, stdout.String() + stderr.String(), code, missing
}

func TestImageBuildBuildsTheImageFromTheFilesBuiltIntoViper(t *testing.T) {
	calls, out, code, missing := imageRun(t, "v1.2.3", "build")
	if code != 0 || len(calls) != 1 || len(missing) != 0 {
		t.Fatalf("code %d, calls %v, files missing in the context: %v\n%s", code, calls, missing, out)
	}
	c := calls[0]
	if c[0] != "docker" || c[1] != "build" || c[2] != "-f" || !strings.HasSuffix(c[3], filepath.Join("image", "Dockerfile")) || c[4] != "-t" || c[5] != "viper-machine:v1.2.3" {
		t.Errorf("%v", c)
	}
	if _, err := os.Stat(c[len(c)-1]); err == nil {
		t.Error("the temporary context is removed afterwards")
	}
	if !strings.Contains(out, "K8S_IMAGE=viper-machine:v1.2.3") || !strings.Contains(out, "--k3d") {
		t.Errorf("the output says what to do next:\n%s", out)
	}
}

func TestImageBuildCanPutTheImageIntoAK3dClusterAndRefusesWhatItDoesNotUnderstand(t *testing.T) {
	calls, out, code, _ := imageRun(t, "v1.2.3-4-gabc-dirty", "build", "--tag", "viper-machine:mine", "--k3d", "viper")
	if code != 0 || len(calls) != 2 || strings.Join(calls[1], " ") != "k3d image import viper-machine:mine -c viper" {
		t.Fatalf("%d %v\n%s", code, calls, out)
	}
	if calls, _, _, _ = imageRun(t, "v1.2.3-4-gabc-dirty", "build"); calls[0][5] != "viper-machine:v1.2.3-4-gabc-dirty" {
		t.Errorf("a version with a build suffix is a valid tag: %v", calls[0])
	}
	if calls, _, code, _ = imageRun(t, "v1", "build", "--tag", "localhost:5000/team/viper-machine:v1"); code != 0 || calls[0][5] != "localhost:5000/team/viper-machine:v1" {
		t.Errorf("a registry with a port: %d %v", code, calls)
	}
	for name, args := range map[string][]string{
		"a tag docker would read as an option": {"build", "--tag", "-v/x:1"},
		"a tag with a blank":                   {"build", "--tag", "a b"},
		"a cluster name with a blank":          {"build", "--k3d", "a b"},
		"another subcommand":                   {"push"},
		"no subcommand":                        nil,
	} {
		if calls, out, code, _ := imageRun(t, "v1", args...); code == 0 || len(calls) != 0 {
			t.Errorf("%s was accepted: %v\n%s", name, calls, out)
		}
	}
}

func TestImageBuildSaysWhatIsMissing(t *testing.T) {
	_, h := fakeTools(t)
	var stderr bytes.Buffer
	h.Stdout, h.Stderr = &bytes.Buffer{}, &stderr
	h.LookPath = func(p string) (string, error) {
		if p == "docker" {
			return "", errors.New("not found")
		}
		return "/bin/" + p, nil
	}
	if code := cli.Main("v1", []string{"image", "build"}, h); code == 0 || !strings.Contains(stderr.String(), "docker not found") {
		t.Errorf("%d %s", code, stderr.String())
	}
}

// registryRun runs viper image build --k3d-registry reg against a docker and a k3d that are scripted: registry says
// whether k3d-reg runs, stopped whether docker start brings it back, k3d whether the k3d program is there.
func registryRun(t *testing.T, registry, stopped, k3d bool, extra ...string) (calls [][]string, out string, code int) {
	t.Helper()
	_, h := fakeTools(t)
	var stdout, stderr bytes.Buffer
	h.Stdout, h.Stderr = &stdout, &stderr
	h.LookPath = func(p string) (string, error) {
		if p == "k3d" && !k3d {
			return "", errors.New("not found")
		}
		return "/bin/" + p, nil
	}
	h.RunTool = func(name string, a ...string) error {
		calls = append(calls, append([]string{name}, a...))
		switch {
		case name == "docker" && a[0] == "start":
			if !stopped {
				return errors.New("No such container: k3d-reg")
			}
			registry = true
		case name == "k3d" && a[0] == "registry":
			registry = true
		}
		return nil
	}
	h.OutputTool = func(name string, a ...string) (string, error) {
		if name == "docker" && a[0] == "port" && a[1] == "k3d-reg" && a[2] == "5000/tcp" {
			if registry {
				return "0.0.0.0:5099\n[::]:5099\n", nil
			}
			return "", errors.New("No such container: k3d-reg")
		}
		return "", errors.New("unexpected " + name)
	}
	args := append([]string{"image", "build", "--tag", "viper-machine:mine", "--k3d-registry", "reg"}, extra...)
	code = cli.Main("v1", args, h)
	return calls, stdout.String() + stderr.String(), code
}

func TestImageBuildPushesIntoAK3dRegistryThatRuns(t *testing.T) {
	calls, out, code := registryRun(t, true, false, true)
	if code != 0 || len(calls) != 3 || calls[0][1] != "build" ||
		strings.Join(calls[1], " ") != "docker tag viper-machine:mine localhost:5099/viper-machine:mine" ||
		strings.Join(calls[2], " ") != "docker push localhost:5099/viper-machine:mine" {
		t.Fatalf("%d %v\n%s", code, calls, out)
	}
	if !strings.Contains(out, "K8S_IMAGE=k3d-reg:5000/viper-machine:mine") || !strings.Contains(out, "--registry-use k3d-reg:5000") ||
		!strings.Contains(out, "COMPOSE_IMAGE=viper-machine:mine") {
		t.Errorf("the output names the image the nodes pull, and the local one for Compose:\n%s", out)
	}
}

func TestImageBuildMakesTheK3dRegistryWhenItIsMissingBeforeItBuilds(t *testing.T) {
	calls, out, code := registryRun(t, false, false, true)
	if code != 0 || len(calls) != 5 || calls[0][0] != "docker" || calls[0][1] != "start" ||
		strings.Join(calls[1], " ") != "k3d registry create reg" || calls[2][1] != "build" {
		t.Fatalf("the registry is made first, without a fixed port (k3d picks a free one): %d %v\n%s", code, calls, out)
	}
	if !strings.Contains(out, "does not exist: making it") || !strings.Contains(out, "k3d-reg:5000/viper-machine:mine") {
		t.Errorf("%s", out)
	}
}

func TestImageBuildStartsAStoppedK3dRegistryInsteadOfMakingANewOne(t *testing.T) {
	calls, out, code := registryRun(t, false, true, true)
	for _, c := range calls {
		if c[0] == "k3d" {
			t.Fatalf("a registry that exists was made again: %v", calls)
		}
	}
	if code != 0 || !strings.Contains(out, "was stopped; it is running again") {
		t.Fatalf("%d %v\n%s", code, calls, out)
	}
}

func TestImageBuildSaysEarlyThatK3dIsMissingAndRefusesABadRegistryName(t *testing.T) {
	calls, out, code := registryRun(t, false, false, false)
	if code == 0 || !strings.Contains(out, "k3d not found") {
		t.Fatalf("%d\n%s", code, out)
	}
	for _, c := range calls {
		if len(c) > 1 && c[1] == "build" {
			t.Fatalf("it built for minutes before it said k3d is missing: %v", calls)
		}
	}
	_, h := fakeTools(t)
	var stderr bytes.Buffer
	h.Stdout, h.Stderr = &bytes.Buffer{}, &stderr
	if code := cli.Main("v1", []string{"image", "build", "--k3d-registry", "a b"}, h); code == 0 || !strings.Contains(stderr.String(), "--k3d-registry") {
		t.Errorf("%d %s", code, stderr.String())
	}
}
