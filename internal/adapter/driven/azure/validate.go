package azure

import (
	"context"
	"fmt"

	"github.com/pt9912/hetzner-viper/internal/model"
)

type findings []model.Finding

func (f *findings) add(l model.Level, format string, a ...any) {
	*f = append(*f, model.Finding{Level: l, Text: fmt.Sprintf(format, a...)})
}

// Validate reads Azure and says whether the server can be created: the subscription and the
// credentials work, the VM size exists in the location, the image exists.
func (c *Client) Validate(ctx context.Context, ch model.Check) []model.Finding {
	var f findings
	if c.opts.SubscriptionID == "" {
		f.add(model.Fail, "AZURE_SUBSCRIPTION_ID is not set (the Azure subscription to create the server in)")
		return f
	}
	api, err := c.client(ctx)
	if err != nil {
		f.add(model.Fail, "Azure API not usable (credentials, subscription, network): %s", c.Describe(err))
		return f
	}
	srv, err := api.VM(ctx, ch.ServerName)
	if err != nil {
		f.add(model.Fail, "Azure API not usable (credentials, subscription, network): %s", c.Describe(err))
		return f
	}
	f.add(model.OK, "Azure credentials can read subscription %s (write access is only seen when viper up creates something)", c.opts.SubscriptionID)
	c.checkSize(ctx, api, &f, ch)
	switch subnet, err := api.Subnet(ctx); {
	case err != nil:
		f.add(model.Fail, "the subnet could not be read: %s", c.Describe(err))
	case subnet == "":
		f.add(model.Info, "resource group %s with a virtual network will be created", c.opts.group())
	default:
		f.add(model.OK, "subnet %s", subnet)
	}
	f.add(model.Info, "login key goes with the VM (Azure keeps no key objects)")
	if srv != nil {
		f.add(model.Info, "server %s exists; viper up reuses it", ch.ServerName)
	} else {
		f.add(model.Info, "server %s will be created", ch.ServerName)
	}
	return f
}

func (c *Client) checkSize(ctx context.Context, api API, f *findings, ch model.Check) {
	size := ch.Spec.Size
	sz, err := api.VMSize(ctx, size)
	switch {
	case err != nil:
		f.add(model.Fail, "VM size %s could not be read: %s", size, c.Describe(err))
		return
	case sz == nil:
		f.add(model.Fail, "VM size %s does not exist in %s (see the VM sizes of the location in the portal)", size, c.opts.Location)
		return
	}
	arch := "x86_64"
	if armSize(size) {
		arch = "arm64"
	}
	f.add(model.OK, "VM size %s exists (%d vCPU, %.4g GB RAM), architecture %s", size, sz.VCPUs, float64(sz.MemoryMB)/1024, arch)
	img, err := parseImage(ch.Spec.Image, armSize(size))
	if err != nil {
		f.add(model.Fail, "%s", err.Error())
		return
	}
	switch ok, err := api.ImageExists(ctx, img); {
	case err != nil:
		f.add(model.Fail, "image %s could not be read: %s", img, c.Describe(err))
	case !ok:
		f.add(model.Fail, "image %s does not exist in %s", img, c.opts.Location)
	default:
		f.add(model.OK, "image %s is %s", ch.Spec.Image, img)
	}
}
