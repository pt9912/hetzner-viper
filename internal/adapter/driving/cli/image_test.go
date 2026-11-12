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
