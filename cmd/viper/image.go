package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	imageRepo       = "ghcr.io/pt9912/hetzner-viper"
	localImage      = "hetzner-claude-run"
	mirrorBaseImage = "mirror.gcr.io/library/debian:bookworm-slim"
)

var releaseTag = regexp.MustCompile(`^v[0-9][A-Za-z0-9_.-]*$`)

// The image of this release is pulled if it is not there yet. A development build, or --local,
// builds the image from the Dockerfile of the checkout. --image names the image to run.
func ensureImage(o options) (string, error) {
	switch {
	case o.image != "":
		return pulled(o.image)
	case releaseTag.MatchString(version) && !o.local:
		return pulled(imageRepo + ":" + version)
	}
	return localImage, build(o)
}

func pulled(ref string) (string, error) {
	if dockerQuiet("image", "inspect", ref) == nil {
		return ref, nil
	}
	fmt.Fprintln(os.Stderr, "+ docker pull", ref)
	if err := dockerRun("pull", ref); err != nil {
		return "", fmt.Errorf("cannot pull %s; build the image from the checkout with --local", ref)
	}
	return ref, nil
}

// Without --base-image the base image comes from Docker Hub; if that is not reachable over IPv4
// (some networks block its AWS addresses), Google's mirror of the same image is used instead.
func build(o options) error {
	dir := o.dir
	if dir == "" {
		dir = "."
	}
	args := []string{"build", "--target", "run"}
	base := o.baseImage
	if base == "" && !hubReachable() {
		base = mirrorBaseImage
		fmt.Fprintf(os.Stderr, "Docker Hub is not reachable; building from %s (use --base-image to choose another source)\n", base)
	}
	if base != "" {
		args = append(args, "--build-arg", "BASE_IMAGE="+base)
	}
	args = append(args, "-t", localImage, filepath.Clean(dir))
	fmt.Fprintln(os.Stderr, "+ docker", strings.Join(args, " "))
	return dockerRun(args...)
}

var hubReachable = func() bool {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		},
	}}
	resp, err := client.Get("https://registry-1.docker.io/v2/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
