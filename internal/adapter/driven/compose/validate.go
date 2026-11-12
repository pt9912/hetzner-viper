package compose

import (
	"context"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
)

type findings []model.Finding

func (f *findings) add(l model.Level, format string, a ...any) {
	*f = append(*f, model.Finding{Level: l, Text: fmt.Sprintf(format, a...)})
}

// manyNetworks is how many Docker networks make the preflight warn: every network takes a /16 of the address pool of the
// daemon, which is used up at about thirty (docs/plan/compose.md §7.13).
const manyNetworks = 20

// Validate reads the Docker host and says whether the server can be made: docker answers, the context is a local daemon,
// the compose plugin and the image are there, the size is fine. Nothing is changed.
func (c *Client) Validate(ctx context.Context, ch model.Check) []model.Finding {
	var f findings
	ver, err := c.docker(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		f.add(model.Fail, "Docker not usable (is it running? context %q, your rights on the socket): %s", c.opts.Context, c.Describe(err))
		return f
	}
	f.add(model.OK, "Docker host %s answers (Docker %s)", c.opts.Context, strings.TrimSpace(ver))
	c.checkLocal(ctx, &f)
	if v, err := c.docker(ctx, "compose", "version", "--short"); err != nil {
		f.add(model.Fail, "the docker compose plugin is missing: %s", c.Describe(err))
	} else {
		f.add(model.OK, "docker compose %s", strings.TrimSpace(v))
	}
	if _, _, err := sizeOf(ch.Spec.Size); err != nil {
		f.add(model.Fail, "%s", err)
	} else {
		f.add(model.OK, "size %s", ch.Spec.Size)
	}
	c.checkImage(ctx, &f)
	c.checkDocker(&f)
	c.checkNetworks(ctx, &f)
	switch srv, err := c.ServerByName(ctx, ch.ServerName); {
	case err != nil:
		f.add(model.Fail, "server %s: %s", ch.ServerName, c.Describe(err))
	case srv != nil:
		f.add(model.Info, "server %s exists; viper up reuses it", ch.ServerName)
	default:
		f.add(model.Info, "server %s will be created", ch.ServerName)
	}
	return f
}

// checkLocal: version 1 keeps the keys as files that the daemon mounts, so the daemon must be on this computer.
func (c *Client) checkLocal(ctx context.Context, f *findings) {
	out, err := c.docker(ctx, "context", "inspect", c.opts.Context, "--format", `{{(index .Endpoints "docker").Host}}`)
	if err != nil {
		f.add(model.Warn, "the endpoint of the Docker context could not be read: %s", c.Describe(err))
		return
	}
	host := strings.TrimSpace(out)
	if strings.HasPrefix(host, "unix://") || strings.HasPrefix(host, "npipe://") {
		return
	}
	f.add(model.Fail, "the Docker context %s is %s: only a daemon on this computer is supported (the keys are files the daemon mounts)", c.opts.Context, host)
}

func (c *Client) checkImage(ctx context.Context, f *findings) {
	if c.opts.Image == "" {
		f.add(model.Fail, "COMPOSE_IMAGE is not set: the image of the machine (viper image build builds it)")
		return
	}
	if _, err := c.docker(ctx, "image", "inspect", "--format", "{{.Id}}", "--", c.opts.Image); err != nil {
		f.add(model.Fail, "image %s is not on the Docker host: viper image build builds it", c.opts.Image)
		return
	}
	f.add(model.OK, "image %s", c.opts.Image)
}

func (c *Client) checkDocker(f *findings) {
	switch c.opts.docker() {
	case DockerDind:
		if c.opts.DockerExplicit {
			f.add(model.Warn, "COMPOSE_DOCKER=dind runs a privileged Docker daemon next to the machine: root on the host if it is broken into; do not use it on a shared host")
		} else {
			f.add(model.Fail, "the machine gets a privileged Docker daemon (root on the host if it is broken into): set COMPOSE_DOCKER=dind to accept that, or remote or none")
		}
	case DockerRemote:
		if c.opts.DockerHost == "" {
			f.add(model.Fail, "COMPOSE_DOCKER=remote needs COMPOSE_DOCKER_HOST (where the Docker daemon is, like tcp://host:2375)")
		} else {
			f.add(model.OK, "Docker through %s", c.opts.DockerHost)
		}
	case DockerNone:
		f.add(model.Info, "COMPOSE_DOCKER=none: no Docker on the machine")
	default:
		f.add(model.Fail, "COMPOSE_DOCKER is dind, remote or none: %s", c.opts.Docker)
	}
}

func (c *Client) checkNetworks(ctx context.Context, f *findings) {
	out, err := c.docker(ctx, "network", "ls", "--quiet")
	if err != nil {
		return
	}
	if n := len(strings.Fields(out)); n >= manyNetworks {
		f.add(model.Warn, "the Docker host has %d networks; each takes a /16 from the address pool, which holds about 30: remove unused ones (docker network prune) or set default-address-pools", n)
	}
}
