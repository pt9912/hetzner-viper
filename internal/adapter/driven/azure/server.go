package azure

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
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
	return &model.Server{ID: v.Name, Name: v.Name, IPv4: v.IP, Created: v.Created}
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
	size, err := api.VMSize(ctx, s.Size)
	if err != nil {
		return nil, err
	}
	if size == nil {
		return nil, fmt.Errorf("VM size %s not found in %s", s.Size, c.opts.Location)
	}
	img, err := parseImage(s.Image, armSize(s.Size))
	if err != nil {
		return nil, err
	}
	if ok, err := api.ImageExists(ctx, img); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("image %s not found in %s", img, c.opts.Location)
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
	err = api.CreateVM(ctx, Launch{
		Name: s.Name, Size: s.Size, Image: img, SubnetID: subnet, NSG: s.Firewall,
		UserData: s.UserData, SSHKey: s.SSHKey, DiskGB: c.opts.diskGB(),
	})
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
