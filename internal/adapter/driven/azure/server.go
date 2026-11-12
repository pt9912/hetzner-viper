package azure

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

var (
	ubuntuRe = regexp.MustCompile(`^ubuntu-(\d\d\.\d\d)$`)
	armRe    = regexp.MustCompile(`^Standard_[A-Z]+\d+[a-z]*p[a-z]*_v\d+$`)
)

// armSize reports whether a VM size is an Arm one (Dpsv5, Dplsv5, Epsv5, Dpdsv6, ...).
func armSize(size string) bool { return armRe.MatchString(size) }

// parseImage resolves SERVER_IMAGE: "ubuntu-24.04" is Canonical's image for the architecture;
// publisher:offer:sku:version names any marketplace image.
func parseImage(setting string, arm bool) (Image, error) {
	if parts := strings.Split(setting, ":"); len(parts) == 4 {
		return Image{parts[0], parts[1], parts[2], parts[3]}, nil
	}
	m := ubuntuRe.FindStringSubmatch(setting)
	switch {
	case m != nil && m[1] == "24.04" && arm:
		return Image{"Canonical", "ubuntu-24_04-lts", "server-arm64", "latest"}, nil
	case m != nil && m[1] == "24.04":
		return Image{"Canonical", "ubuntu-24_04-lts", "server", "latest"}, nil
	case m != nil && m[1] == "22.04" && !arm:
		return Image{"Canonical", "0001-com-ubuntu-server-jammy", "22_04-lts-gen2", "latest"}, nil
	}
	return Image{}, fmt.Errorf("image %s is not supported for this VM size: use ubuntu-24.04 (or ubuntu-22.04 on x86), or publisher:offer:sku:version", setting)
}

func serverFrom(v *VM) *model.Server {
	return &model.Server{ID: v.Name, Name: v.Name, IPv4: v.IP, Created: v.Created, PrivateIP: v.PrivateIP, GroupRuleMissing: !inGroup(v)}
}

// ServerByName returns the VM of that name in the resource group, nil if there is none.
func (c *Client) ServerByName(ctx context.Context, name string) (*model.Server, error) {
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	v, err := api.VM(ctx, name)
	if err != nil || v == nil {
		return nil, err
	}
	return serverFrom(v), nil
}

// CreateServer makes the VM and waits until it has its public address.
func (c *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	img, err := c.resolve(ctx, api, s)
	if err != nil {
		return nil, err
	}
	subnet, err := api.Subnet(ctx)
	if err != nil {
		return nil, err
	}
	if subnet == "" {
		return nil, fmt.Errorf("no subnet in resource group %s: the firewall step makes it", c.opts.group())
	}
	nsg, err := api.NSG(ctx, s.Firewall)
	if err != nil {
		return nil, err
	}
	if nsg == nil {
		return nil, fmt.Errorf("network security group %s not found", s.Firewall)
	}
	l := Launch{
		Name: s.Name, Size: s.Size, Image: img, SubnetID: subnet, NSG: s.Firewall,
		UserData: s.UserData, SSHKey: s.SSHKey, DiskGB: c.opts.diskGB(), Tags: s.Labels,
	}
	if l.Group, err = c.groupAtStart(ctx, api, s); err != nil {
		return nil, err
	}
	err = api.CreateVM(ctx, l)
	if err != nil {
		return nil, err
	}
	var up *VM
	err = c.wait(ctx, "the VM to have its address", func() (bool, error) {
		v, err := api.VM(ctx, s.Name)
		if err != nil || v == nil {
			return false, err
		}
		up = v
		return v.IP != "", nil
	})
	if err != nil {
		return nil, err
	}
	return serverFrom(up), nil
}

// DeleteServer deletes the VM with its network interface and public address and waits.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	api, err := c.client(ctx)
	if err != nil {
		return err
	}
	return api.DeleteVM(ctx, id)
}

// groupAtStart prepares the start of a server of a NATS group: its network security group gets the rule of the
// group. It returns the ID of the application security group the interface goes into, "" for a server of no group.
func (c *Client) groupAtStart(ctx context.Context, api *session, s model.ServerSpec) (string, error) {
	if s.Network == "" {
		return "", nil
	}
	asg, err := api.GroupASG(ctx, s.Network)
	if err != nil {
		return "", err
	}
	if asg == nil {
		return "", fmt.Errorf("the application security group %s of the NATS group does not exist", s.Network)
	}
	group := strings.TrimPrefix(s.Network, natsPrefix)
	if err := api.AllowGroup(ctx, s.Firewall, group, asg.ID, c.opts.natsPort()); err != nil {
		return "", &cloud.OpError{Op: "adding the NATS rule to the network security group " + s.Firewall, Write: true, Err: err}
	}
	return asg.ID, nil
}

// resolve checks that the VM size and the image exist and returns the image.
func (c *Client) resolve(ctx context.Context, api *session, s model.ServerSpec) (Image, error) {
	size, err := api.VMSize(ctx, s.Size)
	if err != nil {
		return Image{}, err
	}
	if size == nil {
		return Image{}, fmt.Errorf("VM size %s not found in %s", s.Size, c.opts.Location)
	}
	img, err := parseImage(s.Image, armSize(s.Size))
	if err != nil {
		return Image{}, err
	}
	if ok, err := api.ImageExists(ctx, img); err != nil {
		return Image{}, err
	} else if !ok {
		return Image{}, fmt.Errorf("image %s not found in %s", img, c.opts.Location)
	}
	return img, nil
}
