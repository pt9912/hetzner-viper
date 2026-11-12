package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	hetznerviper "github.com/pt9912/hetzner-viper"
)

// imageTagRe is a Docker image reference as viper writes it into a command: a repository and a tag, nothing that a
// shell or docker would read as an option.
var imageTagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9_][A-Za-z0-9_.-]*)?$`)

// k3dClusterRe is the name of a k3d cluster.
var k3dClusterRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// nativeImage is viper image build: it builds the image of the Kubernetes provider (docs/plan/kubernetes.md) with Docker
// from the files built into viper, so that a user of a downloaded viper needs neither a checkout nor make. With --k3d it
// then puts the image into that k3d cluster, which has no way to pull a local image.
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
	fmt.Fprintf(h.Stdout, "Image %s is ready. Use it with K8S_IMAGE=%s in the settings of the machine.\n", tag, tag)
	if o.k3d == "" {
		fmt.Fprintln(h.Stdout, "A cluster pulls it from a registry: push it there (docker push) and name the registry's reference in K8S_IMAGE; k3d: add --k3d CLUSTER.")
	}
	return nil
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
