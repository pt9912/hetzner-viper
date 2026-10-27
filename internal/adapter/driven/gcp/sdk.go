package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// sdk is the API over the compute/v1 client of the project and zone.
type sdk struct {
	svc           *compute.Service
	project, zone string
	poll          time.Duration
}

func newSDK(ctx context.Context, o Options) (*sdk, error) {
	svc, err := compute.NewService(ctx)
	if err != nil {
		return nil, err
	}
	return &sdk{svc: svc, project: o.Project, zone: o.Zone, poll: o.poll()}, nil
}

func notFound(err error) bool {
	var ge *googleapi.Error
	return errors.As(err, &ge) && ge.Code == http.StatusNotFound
}

// waitZone and waitGlobal wait for an operation to be done and turn its error into an error.
func (s *sdk) waitZone(ctx context.Context, op *compute.Operation) error {
	return s.waitOp(ctx, op, func() (*compute.Operation, error) {
		return s.svc.ZoneOperations.Get(s.project, s.zone, op.Name).Context(ctx).Do()
	})
}

func (s *sdk) waitGlobal(ctx context.Context, op *compute.Operation) error {
	return s.waitOp(ctx, op, func() (*compute.Operation, error) {
		return s.svc.GlobalOperations.Get(s.project, op.Name).Context(ctx).Do()
	})
}

func (s *sdk) waitOp(ctx context.Context, op *compute.Operation, get func() (*compute.Operation, error)) error {
	for {
		if op.Status == "DONE" {
			if op.Error != nil && len(op.Error.Errors) > 0 {
				return fmt.Errorf("%s: %s", op.Error.Errors[0].Code, op.Error.Errors[0].Message)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.poll):
		}
		var err error
		if op, err = get(); err != nil {
			return err
		}
	}
}

func (s *sdk) Instance(ctx context.Context, name string) (*Instance, error) {
	i, err := s.svc.Instances.Get(s.project, s.zone, name).Context(ctx).Do()
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := &Instance{Name: i.Name, Status: i.Status}
	if t, err := time.Parse(time.RFC3339, i.CreationTimestamp); err == nil {
		out.Created = t
	}
	for _, n := range i.NetworkInterfaces {
		for _, a := range n.AccessConfigs {
			if a.NatIP != "" && out.IP == "" {
				out.IP = a.NatIP
			}
		}
	}
	return out, nil
}

// diskType is the boot disk type the machine type takes: the newer series (C4, N4, C4A, N4A) have
// Hyperdisk only, the others Persistent Disk.
func diskType(machineType string) string {
	for _, p := range []string{"c4-", "n4-", "c4a-", "n4a-"} {
		if strings.HasPrefix(machineType, p) {
			return "hyperdisk-balanced"
		}
	}
	return "pd-balanced"
}

func (s *sdk) CreateInstance(ctx context.Context, l Launch) error {
	inst := &compute.Instance{
		Name:        l.Name,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", s.zone, l.MachineType),
		Tags:        &compute.Tags{Items: []string{l.Tag}},
		Labels:      map[string]string{"viper": l.Name},
		Disks: []*compute.AttachedDisk{{
			Boot: true, AutoDelete: true,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: l.Image, DiskSizeGb: l.DiskGB, DiskType: fmt.Sprintf("zones/%s/diskTypes/%s", s.zone, diskType(l.MachineType)),
			},
		}},
		NetworkInterfaces: []*compute.NetworkInterface{{
			Network:       "global/networks/" + l.Network,
			Subnetwork:    s.subnetwork(l.Subnet),
			AccessConfigs: []*compute.AccessConfig{{Name: "External NAT", Type: "ONE_TO_ONE_NAT"}},
		}},
		Metadata: &compute.Metadata{Items: []*compute.MetadataItems{{Key: "user-data", Value: &l.UserData}}},
	}
	op, err := s.svc.Instances.Insert(s.project, s.zone, inst).Context(ctx).Do()
	if err != nil {
		return err
	}
	return s.waitZone(ctx, op)
}

// subnetwork is the subnet in the region of the zone ("" for none: an auto-mode network picks its own).
func (s *sdk) subnetwork(name string) string {
	if name == "" {
		return ""
	}
	region := s.zone
	if i := strings.LastIndex(region, "-"); i > 0 {
		region = region[:i]
	}
	return fmt.Sprintf("regions/%s/subnetworks/%s", region, name)
}

func (s *sdk) DeleteInstance(ctx context.Context, name string) error {
	op, err := s.svc.Instances.Delete(s.project, s.zone, name).Context(ctx).Do()
	if err != nil {
		return err
	}
	return s.waitZone(ctx, op)
}

func allows(r *compute.Firewall) (ssh, icmp bool) {
	for _, a := range r.Allowed {
		switch a.IPProtocol {
		case "tcp":
			ssh = ssh || slices.Contains(a.Ports, "22")
		case "icmp":
			icmp = true
		}
	}
	return ssh, icmp
}

func (s *sdk) Firewall(ctx context.Context, name string) (*Rule, error) {
	r, err := s.svc.Firewalls.Get(s.project, name).Context(ctx).Do()
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	out := &Rule{Name: r.Name, Ranges: r.SourceRanges}
	out.SSH, out.ICMP = allows(r)
	return out, nil
}

func (s *sdk) CreateFirewall(ctx context.Context, r Rule) error {
	fw := &compute.Firewall{
		Name: r.Name, Description: "viper: SSH and ping to the machines tagged " + r.Tag,
		Network: "global/networks/" + r.Network, Direction: "INGRESS",
		SourceRanges: r.Ranges, TargetTags: []string{r.Tag},
		Allowed: []*compute.FirewallAllowed{{IPProtocol: "tcp", Ports: []string{"22"}}, {IPProtocol: "icmp"}},
	}
	op, err := s.svc.Firewalls.Insert(s.project, fw).Context(ctx).Do()
	if err != nil {
		return err
	}
	return s.waitGlobal(ctx, op)
}

func (s *sdk) AddFirewallAllowed(ctx context.Context, name string, ssh, icmp bool) error {
	r, err := s.svc.Firewalls.Get(s.project, name).Context(ctx).Do()
	if err != nil {
		return err
	}
	allowed := r.Allowed
	if ssh {
		allowed = append(allowed, &compute.FirewallAllowed{IPProtocol: "tcp", Ports: []string{"22"}})
	}
	if icmp {
		allowed = append(allowed, &compute.FirewallAllowed{IPProtocol: "icmp"})
	}
	op, err := s.svc.Firewalls.Patch(s.project, name, &compute.Firewall{Allowed: allowed}).Context(ctx).Do()
	if err != nil {
		return err
	}
	return s.waitGlobal(ctx, op)
}

func (s *sdk) MachineType(ctx context.Context, name string) (*MachineType, error) {
	m, err := s.svc.MachineTypes.Get(s.project, s.zone, name).Context(ctx).Do()
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &MachineType{VCPUs: m.GuestCpus, MemoryMB: m.MemoryMb}, nil
}

func (s *sdk) ImageFamily(ctx context.Context, project, family string) (string, error) {
	img, err := s.svc.Images.GetFromFamily(project, family).Context(ctx).Do()
	if err != nil {
		if notFound(err) {
			return "", nil
		}
		return "", err
	}
	return img.SelfLink, nil
}

func (s *sdk) NetworkExists(ctx context.Context, name string) (bool, error) {
	if _, err := s.svc.Networks.Get(s.project, name).Context(ctx).Do(); err != nil {
		if notFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
