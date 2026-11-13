package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	hetznerviper "github.com/pt9912/hetzner-viper"
)

// imageTagRe is a Docker image reference as viper writes it into a command: a repository and a tag, nothing that a
// shell or docker would read as an option.
var imageTagRe = regexp.MustCompile(`^([a-z0-9][a-z0-9.-]*(:[0-9]{1,5})?/)?[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9_][A-Za-z0-9_.-]*)?$`)

// k3dClusterRe is the name of a k3d cluster.
var k3dClusterRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// nativeImage is viper image build: it builds the image of the Kubernetes provider (docs/plan/kubernetes.md) with Docker
// from the files built into viper, so that a user of a downloaded viper needs neither a checkout nor make. With --k3d it
// then puts the image into that k3d cluster, which has no way to pull a local image; with --k3d-registry it pushes it into
// a registry that k3d made (k3d registry create NAME), which the nodes of any cluster that uses it pull from.
func nativeImage(o *options) error {
	tag, err := imageArgs(o)
	if err != nil {
		return err
	}
	h := o.host()
	for _, p := range imagePrograms(o) {
		if _, err := h.LookPath(p.name); err != nil {
			return errors.New("viper image build: " + p.missing)
		}
	}
	registryPort := ""
	if o.k3dRegistry != "" { // before the build, which takes minutes: a missing k3d or a registry that cannot be made should show first
		if registryPort, err = ensureK3dRegistry(o, "k3d-"+o.k3dRegistry); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp("", "viper-image-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := writeImageContext(dir); err != nil {
		return err
	}
	fmt.Fprintf(h.Stdout, "Building the image %s (several minutes the first time; Docker keeps what it built) ...\n", tag)
	if err := h.RunTool("docker", "build", "-f", filepath.Join(dir, "image", "Dockerfile"), "-t", tag, dir); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	if o.k3d != "" {
		if err := h.RunTool("k3d", "image", "import", tag, "-c", o.k3d); err != nil {
			return fmt.Errorf("k3d image import failed: %w", err)
		}
		fmt.Fprintf(h.Stdout, "The image is in the k3d cluster %s.\n", o.k3d)
	}
	use := tag
	if o.k3dRegistry != "" {
		if use, err = pushToK3dRegistry(o, tag, registryPort); err != nil {
			return err
		}
	}
	fmt.Fprintf(h.Stdout, "Image %s is ready. Use it with K8S_IMAGE=%s (PROVIDER=kubernetes) or COMPOSE_IMAGE=%s (PROVIDER=compose, on the local Docker host) in the settings of the machine.\n", tag, use, tag)
	if o.k3d == "" && o.k3dRegistry == "" {
		fmt.Fprintln(h.Stdout, "A cluster pulls it from a registry: push it there (docker push) and name the registry's reference in K8S_IMAGE; k3d: add --k3d CLUSTER, or --k3d-registry NAME for a registry made with k3d registry create.")
	}
	return nil
}

// k3dRegistryPort is the port a k3d registry listens on inside the Docker network; the nodes of a cluster that uses the
// registry reach it as k3d-<name>:5000.
const k3dRegistryPort = "5000"

// k3dRegistryHostPort is the port the k3d registry has on this computer, "" if there is no running registry of that name.
func k3dRegistryHostPort(o *options, container string) string {
	out, err := o.host().OutputTool("docker", "port", container, k3dRegistryPort+"/tcp")
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	i := strings.LastIndex(line, ":")
	if err != nil || i < 0 || line[i+1:] == "" {
		return ""
	}
	return line[i+1:]
}

// ensureK3dRegistry returns the port of the k3d registry on this computer. A registry that is missing is made (k3d picks a
// free port, so nothing on the computer is in the way); one that was stopped is started. The cluster that is to pull from it
// is not made here: it is made with k3d cluster create --registry-use.
func ensureK3dRegistry(o *options, container string) (string, error) {
	h := o.host()
	if port := k3dRegistryHostPort(o, container); port != "" {
		return port, nil
	}
	if _, err := h.LookPath("k3d"); err != nil {
		return "", errors.New("viper image build: k3d not found (--k3d-registry makes the registry with k3d registry create)")
	}
	if err := h.RunTool("docker", "start", container); err == nil { // it exists but is stopped
		if port := k3dRegistryHostPort(o, container); port != "" {
			fmt.Fprintf(h.Stdout, "The k3d registry %s was stopped; it is running again.\n", container)
			return port, nil
		}
	}
	fmt.Fprintf(h.Stdout, "The k3d registry %s does not exist: making it ...\n", container)
	if err := h.RunTool("k3d", "registry", "create", o.k3dRegistry); err != nil {
		return "", fmt.Errorf("k3d registry create failed: %w", err)
	}
	if port := k3dRegistryHostPort(o, container); port != "" {
		return port, nil
	}
	return "", fmt.Errorf("viper image build: the k3d registry %s has no port on this computer", container)
}

// pushToK3dRegistry pushes the image into the k3d registry o.k3dRegistry (port is its port on this computer) and returns the name the
// nodes of a cluster pull it by (k3d-<name>:5000/...). The registry is a container named k3d-<name>; viper pushes through
// the port it has on this computer, because the name k3d-<name> is only known inside the Docker network. A cluster uses it
// when it was made with k3d cluster create --registry-use k3d-NAME:5000.
func pushToK3dRegistry(o *options, tag, port string) (string, error) {
	h := o.host()
	container := "k3d-" + o.k3dRegistry
	name := path.Base(tag) // viper-machine:<tag>, without the registry and the path of the tag it was built as
	pushed := "localhost:" + port + "/" + name
	for _, args := range [][]string{{"tag", tag, pushed}, {"push", pushed}} {
		if err := h.RunTool("docker", args...); err != nil {
			return "", fmt.Errorf("docker %s failed: %w", args[0], err)
		}
	}
	use := container + ":" + k3dRegistryPort + "/" + name
	fmt.Fprintf(h.Stdout, "The image is in the k3d registry %s. A cluster made with k3d cluster create ... --registry-use %s:%s pulls it as %s.\n", container, container, k3dRegistryPort, use)
	return use, nil
}

// imageArgs checks the command line of viper image build and returns the tag of the image.
func imageArgs(o *options) (string, error) {
	if len(o.args) != 1 || o.args[0] != "build" {
		return "", errors.New("viper image: the only subcommand is build\n" + usageLine("image"))
	}
	tag := o.tag
	if tag == "" {
		tag = "viper-machine:" + imageVersion(o.version)
	}
	if !imageTagRe.MatchString(tag) {
		return "", fmt.Errorf("viper image build: --tag is name:tag in lowercase, like viper-machine:v1: %q", tag)
	}
	if o.k3d != "" && !k3dClusterRe.MatchString(o.k3d) {
		return "", fmt.Errorf("viper image build: --k3d is the name of a k3d cluster: %q", o.k3d)
	}
	if o.k3dRegistry != "" && !k3dClusterRe.MatchString(o.k3dRegistry) {
		return "", fmt.Errorf("viper image build: --k3d-registry is the name of a k3d registry, as in k3d registry create NAME: %q", o.k3dRegistry)
	}
	return tag, nil
}

// imagePrograms are the programs viper image build runs, with what to say if one is missing.
func imagePrograms(o *options) []struct{ name, missing string } {
	ps := []struct{ name, missing string }{{"docker", "docker not found: the image is built with Docker (or use a published image: K8S_IMAGE)"}}
	if o.k3d != "" {
		ps = append(ps, struct{ name, missing string }{"k3d", "k3d not found (--k3d puts the image into a k3d cluster)"})
	}
	return ps
}

// imageVersion is the version as part of a tag: Docker allows letters, digits, "_", "." and "-", and not more than 128.
func imageVersion(v string) string {
	t := regexp.MustCompile(`[^A-Za-z0-9_.-]`).ReplaceAllString(v, "-")
	if t == "" {
		t = "dev"
	}
	if len(t) > 100 {
		t = t[:100]
	}
	return t
}

// writeImageContext puts the files that build the image where docker build finds them: the layout of the repository.
func writeImageContext(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "image"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "cloud-init.yaml"), hetznerviper.CloudInit, 0o644); err != nil {
		return err
	}
	return fs.WalkDir(hetznerviper.ImageContext, "image", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := hetznerviper.ImageContext.ReadFile(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if filepath.Ext(path) == ".sh" || filepath.Ext(path) == ".py" {
			mode = 0o755
		}
		return os.WriteFile(filepath.Join(dir, path), b, mode)
	})
}
