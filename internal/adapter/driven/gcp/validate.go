package gcp

import (
	"context"
	"fmt"

	"github.com/pt9912/hetzner-viper/internal/model"
)

type findings []model.Finding

func (f *findings) add(l model.Level, format string, a ...any) {
	*f = append(*f, model.Finding{Level: l, Text: fmt.Sprintf(format, a...)})
}

// Validate reads Compute Engine and says whether the server can be created: the project and the
// credentials work, the machine type exists in the zone, the image resolves, the network exists.
func (c *Client) Validate(ctx context.Context, ch model.Check) []model.Finding {
	var f findings
	if c.opts.Project == "" {
		f.add(model.Fail, "GCP_PROJECT is not set (the Google Cloud project to create the server in)")
		return f
	}
	api, err := c.client(ctx)
	if err != nil {
		f.add(model.Fail, "Google Cloud API not usable (credentials, project, network): %s", c.Describe(err))
		return f
	}
	srv, err := api.Instance(ctx, ch.ServerName)
	if err != nil {
		f.add(model.Fail, "Google Cloud API not usable (credentials, project, network): %s", c.Describe(err))
		return f
	}
	f.add(model.OK, "Google credentials can read project %s in %s (write access is only seen when viper up creates something)", c.opts.Project, c.opts.Zone)
	c.checkMachine(ctx, api, &f, ch)
	if ok, err := api.NetworkExists(ctx, c.opts.network()); err != nil {
		f.add(model.Fail, "network %s could not be read: %s", c.opts.network(), c.Describe(err))
	} else if !ok {
		f.add(model.Fail, "network %s does not exist in project %s: set GCP_NETWORK", c.opts.network(), c.opts.Project)
	} else {
		f.add(model.OK, "network %s exists", c.opts.network())
	}
	f.add(model.Info, "login key goes into the VM's cloud-init (Google Cloud keeps no key objects)")
	if srv != nil {
		f.add(model.Info, "server %s exists; viper up reuses it", ch.ServerName)
	} else {
		f.add(model.Info, "server %s will be created", ch.ServerName)
	}
	return f
}

func (c *Client) checkMachine(ctx context.Context, api API, f *findings, ch model.Check) {
	size := ch.Spec.Size
	mt, err := api.MachineType(ctx, size)
	switch {
	case err != nil:
		f.add(model.Fail, "machine type %s could not be read: %s", size, c.Describe(err))
		return
	case mt == nil:
		f.add(model.Fail, "machine type %s does not exist in %s (see the machine types of the zone in the console)", size, c.opts.Zone)
		return
	}
	arch := "x86_64"
	if armType(size) {
		arch = "arm64"
	}
	f.add(model.OK, "machine type %s exists (%d vCPU, %.4g GB RAM), architecture %s", size, mt.VCPUs, float64(mt.MemoryMB)/1024, arch)
	switch link, err := image(ctx, api, ch.Spec.Image, armType(size)); {
	case err != nil:
		f.add(model.Fail, "image %s could not be resolved: %s", ch.Spec.Image, c.Describe(err))
	case link == "":
		f.add(model.Fail, "image %s does not exist for %s", ch.Spec.Image, arch)
	default:
		f.add(model.OK, "image %s is %s", ch.Spec.Image, link)
	}
}
