package gcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
)

var ubuntuRe = regexp.MustCompile(`^ubuntu-(\d\d)\.(\d\d)$`)

// armType reports whether a machine type is an Arm one (Tau T2A, C4A, N4A).
func armType(size string) bool {
	for _, p := range []string{"t2a-", "c4a-", "n4a-"} {
		if strings.HasPrefix(size, p) {
			return true
		}
	}
	return false
}

// image resolves SERVER_IMAGE: "ubuntu-24.04" is the newest image of Canonical's family for the
// architecture, "projects/<project>/global/images/<name>" (or .../family/<name>) is used as given.
// "" if there is no such image.
func image(ctx context.Context, api API, setting string, arm bool) (string, error) {
	m := ubuntuRe.FindStringSubmatch(setting)
	if m == nil {
		if !strings.HasPrefix(setting, "projects/") {
			return "", fmt.Errorf("image %s is not supported: use ubuntu-<version>, e.g. ubuntu-24.04, or projects/<project>/global/images/<name>", setting)
		}
		return setting, nil
	}
	v := m[1] + m[2]
	families := []string{"ubuntu-" + v + "-lts-amd64", "ubuntu-" + v + "-lts"}
	if arm {
		families = []string{"ubuntu-" + v + "-lts-arm64"}
	}
	for _, f := range families {
		link, err := api.ImageFamily(ctx, "ubuntu-os-cloud", f)
		if err != nil || link != "" {
			return link, err
		}
	}
	return "", nil
}

func serverFrom(i *Instance) *model.Server {
	return &model.Server{ID: i.Name, Name: i.Name, IPv4: i.IP, Created: i.Created}
}

// ServerByName returns the VM of that name in the zone, nil if there is none.
func (c *Client) ServerByName(ctx context.Context, name string) (*model.Server, error) {
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	i, err := api.Instance(ctx, name)
	if err != nil || i == nil {
		return nil, err
	}
	return serverFrom(i), nil
}

// CreateServer starts the VM and waits until it runs and has a public address.
func (c *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	mt, err := api.MachineType(ctx, s.Size)
	if err != nil {
		return nil, err
	}
	if mt == nil {
		return nil, fmt.Errorf("machine type %s not found in %s", s.Size, c.opts.Zone)
	}
	img, err := image(ctx, api, s.Image, armType(s.Size))
	if err != nil {
		return nil, err
	}
	if img == "" {
		return nil, fmt.Errorf("image %s not found", s.Image)
	}
	if ok, err := api.NetworkExists(ctx, c.opts.network()); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("network %s not found in project %s: set GCP_NETWORK", c.opts.network(), c.opts.Project)
	}
	err = api.CreateInstance(ctx, Launch{
		Name: s.Name, MachineType: s.Size, Image: img, Network: c.opts.network(), Subnet: c.opts.Subnet, Tag: s.Firewall,
		UserData: s.UserData, DiskGB: c.opts.diskGB(),
	})
	if err != nil {
		return nil, err
	}
	var up *Instance
	err = c.wait(ctx, "the VM to run", func() (bool, error) {
		i, err := api.Instance(ctx, s.Name)
		if err != nil || i == nil {
			return false, err
		}
		up = i
		switch i.Status {
		case "STOPPING", "STOPPED", "TERMINATED", "SUSPENDING", "SUSPENDED":
			return false, fmt.Errorf("the VM %s is %s instead of RUNNING", s.Name, i.Status)
		}
		return i.Status == "RUNNING" && i.IP != "", nil
	})
	if err != nil {
		return nil, err
	}
	return serverFrom(up), nil
}

// DeleteServer deletes the VM and waits until it is gone.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	api, err := c.client(ctx)
	if err != nil {
		return err
	}
	return api.DeleteInstance(ctx, id)
}
