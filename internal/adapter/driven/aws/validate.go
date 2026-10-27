package aws

import (
	"context"
	"fmt"

	"github.com/pt9912/hetzner-viper/internal/model"
)

type findings []model.Finding

func (f *findings) add(l model.Level, format string, a ...any) {
	*f = append(*f, model.Finding{Level: l, Text: fmt.Sprintf(format, a...)})
}

// Validate reads EC2 and says whether the server can be created: the credentials work, the
// instance type exists in the region, the image resolves, there is a subnet, the login key fits.
func (c *Client) Validate(ctx context.Context, ch model.Check) []model.Finding {
	var f findings
	srv, err := c.ServerByName(ctx, ch.ServerName)
	if err != nil {
		f.add(model.Fail, "AWS API not usable (credentials, region, network): %s", c.Describe(err))
		return f
	}
	f.add(model.OK, "AWS credentials can read EC2 in %s (write access is only seen when viper up creates something)", c.opts.region())
	c.checkInstance(ctx, &f, ch)
	if n, err := c.network(ctx); err != nil {
		f.add(model.Fail, "subnets could not be read: %s", c.Describe(err))
	} else if n == nil {
		f.add(model.Fail, "no default subnet in %s: set AWS_SUBNET_ID (or AWS_VPC_ID)", c.opts.Location)
	} else {
		f.add(model.OK, "subnet %s in %s (VPC %s)", n.subnet, n.zone, n.vpc)
	}
	c.checkKey(ctx, &f, ch)
	if srv != nil {
		f.add(model.Info, "server %s exists; viper up reuses it", ch.ServerName)
	} else {
		f.add(model.Info, "server %s will be created", ch.ServerName)
	}
	return f
}

func (c *Client) checkInstance(ctx context.Context, f *findings, ch model.Check) {
	size, img := ch.Spec.Size, ch.Spec.Image
	it, err := c.instanceType(ctx, size)
	switch {
	case err != nil:
		f.add(model.Fail, "instance type %s could not be read: %s", size, c.Describe(err))
		return
	case it == nil:
		f.add(model.Fail, "instance type %s does not exist in %s (see the instance types in the EC2 console)", size, c.opts.region())
		return
	}
	f.add(model.OK, "instance type %s exists (%d vCPU, %.4g GB RAM), architecture %s", size, it.vcpus, float64(it.memMiB)/1024, it.arch)
	switch got, err := c.image(ctx, img, it.arch); {
	case err != nil:
		f.add(model.Fail, "image %s could not be resolved: %s", img, c.Describe(err))
	case got == nil:
		f.add(model.Fail, "image %s does not exist for %s in %s", img, it.arch, c.opts.region())
	default:
		f.add(model.OK, "image %s is %s", img, got.id)
	}
}

// checkKey compares the login key with the key pairs of the region.
func (c *Client) checkKey(ctx context.Context, f *findings, ch model.Check) {
	ks, err := c.keys(ctx)
	if err != nil {
		f.add(model.Fail, "key pairs could not be listed: %s", c.Describe(err))
		return
	}
	fp := fingerprint(ch.PublicKey)
	existing, named := "", false
	for _, k := range ks {
		if existing == "" && sameFingerprint(str(k.KeyFingerprint), fp) {
			existing = str(k.KeyName)
		}
		if str(k.KeyName) == ch.KeyName {
			named = true
		}
	}
	switch {
	case existing != "":
		f.add(model.OK, "login key already in %s as \"%s\" (reused)", c.opts.region(), existing)
	case named:
		f.add(model.Fail, "key pair %s in AWS belongs to a different public key than %s.pub", ch.KeyName, ch.KeyFile)
	default:
		f.add(model.Info, "login key will be uploaded as %s", ch.KeyName)
	}
}
