package aws

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// network is where the server goes.
type network struct{ subnet, vpc, zone string }

// network finds the subnet: AWS_SUBNET_ID, else a default subnet (of the zone, if SERVER_LOCATION
// names one; of AWS_VPC_ID, if that is set). nil if there is none.
func (c *Client) network(ctx context.Context) (*network, error) {
	c.mu.Lock()
	cached := c.net
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	in := &ec2.DescribeSubnetsInput{}
	switch {
	case c.opts.SubnetID != "":
		in.SubnetIds = []string{c.opts.SubnetID}
	default:
		if c.opts.VPCID != "" {
			in.Filters = append(in.Filters, filter("vpc-id", c.opts.VPCID))
		} else {
			in.Filters = append(in.Filters, filter("default-for-az", "true"))
		}
		if z := c.opts.zone(); z != "" {
			in.Filters = append(in.Filters, filter("availability-zone", z))
		}
	}
	out, err := a.ec2.DescribeSubnets(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(out.Subnets) == 0 {
		return nil, nil
	}
	// a subnet that gives its instances a public address first (a server in a private one cannot be
	// reached), then by zone
	sort.Slice(out.Subnets, func(i, j int) bool {
		pi, pj := out.Subnets[i].MapPublicIpOnLaunch != nil && *out.Subnets[i].MapPublicIpOnLaunch, out.Subnets[j].MapPublicIpOnLaunch != nil && *out.Subnets[j].MapPublicIpOnLaunch
		if pi != pj {
			return pi
		}
		return str(out.Subnets[i].AvailabilityZone) < str(out.Subnets[j].AvailabilityZone)
	})
	s := out.Subnets[0]
	n := &network{subnet: str(s.SubnetId), vpc: str(s.VpcId), zone: str(s.AvailabilityZone)}
	c.mu.Lock()
	c.net = n
	c.mu.Unlock()
	return n, nil
}

func filter(name string, values ...string) types.Filter {
	return types.Filter{Name: ptr(name), Values: values}
}

// awsTags are the tags of AWS_TAGS.
func awsTags(s string) ([]types.Tag, error) {
	parsed, err := model.ParseAWSTags(s)
	var out []types.Tag
	for _, t := range parsed {
		out = append(out, types.Tag{Key: ptr(t.Key), Value: ptr(t.Value)})
	}
	return out, err
}

func tags(name string) []types.Tag {
	return []types.Tag{{Key: ptr("Name"), Value: ptr(name)}, {Key: ptr("viper"), Value: ptr(name)}}
}

// securityGroup returns the ID of the group of that name in the VPC, "" if there is none.
func (c *Client) securityGroup(ctx context.Context, name, vpc string) (string, []types.IpPermission, error) {
	a, err := c.api(ctx)
	if err != nil {
		return "", nil, err
	}
	out, err := a.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: []types.Filter{filter("group-name", name), filter("vpc-id", vpc)}})
	if err != nil {
		return "", nil, err
	}
	if len(out.SecurityGroups) == 0 {
		return "", nil, nil
	}
	return str(out.SecurityGroups[0].GroupId), out.SecurityGroups[0].IpPermissions, nil
}

// EnsureFirewall makes sure the security group exists and lets SSH (TCP 22) and ping in from the
// networks. A missing rule is added (also to an existing group); other rules are left alone.
func (c *Client) EnsureFirewall(ctx context.Context, name string, sources []string) (model.FirewallResult, error) {
	var res model.FirewallResult
	if !hasNetwork(sources) {
		return res, &cloud.OpError{Op: "reading the networks", Err: fmt.Errorf("SSH_ALLOWED_CIDRS names no network")}
	}
	a, err := c.api(ctx)
	if err != nil {
		return res, &cloud.OpError{Op: "reaching AWS", Err: err}
	}
	n, err := c.network(ctx)
	if err != nil {
		return res, &cloud.OpError{Op: "finding the subnet", Err: err}
	}
	if n == nil {
		return res, &cloud.OpError{Op: "finding the subnet", Err: fmt.Errorf("no default subnet: set AWS_SUBNET_ID")}
	}
	id, perms, created, err := c.ensureGroup(ctx, a, name, n.vpc)
	res.Created = created
	if err != nil {
		return res, err
	}
	has22, hasICMP := hasRules(perms)
	res.Complete = has22 && hasICMP
	if !has22 {
		if err := allow(ctx, a, id, name, "tcp", 22, "SSH", sources); err != nil {
			return res, err
		}
	}
	if !hasICMP {
		if err := allow(ctx, a, id, name, "icmp", -1, "ICMP", sources); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ensureGroup finds the security group of that name in the VPC or creates it.
func (c *Client) ensureGroup(ctx context.Context, a *apis, name, vpc string) (id string, perms []types.IpPermission, created bool, err error) {
	id, perms, err = c.securityGroup(ctx, name, vpc)
	if err != nil {
		return "", nil, false, &cloud.OpError{Op: "listing the security groups", Err: err}
	}
	if id != "" {
		return id, perms, false, nil
	}
	out, err := a.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: ptr(name), Description: ptr("viper: SSH access"), VpcId: ptr(vpc),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeSecurityGroup, Tags: tags(name)}},
	})
	if err != nil {
		return "", nil, false, &cloud.OpError{Op: fmt.Sprintf("creating the security group %s", name), Write: true, Err: err}
	}
	return str(out.GroupId), nil, true, nil
}

// hasNetwork reports whether any of the sources is a network (not blank).
func hasNetwork(sources []string) bool {
	for _, s := range sources {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// hasRules reports whether the permissions let SSH and ping in (from anywhere).
func hasRules(perms []types.IpPermission) (ssh, icmp bool) {
	for _, p := range perms {
		proto := str(p.IpProtocol)
		ssh = ssh || (proto == "tcp" && p.FromPort != nil && *p.FromPort == 22 && p.ToPort != nil && *p.ToPort == 22)
		icmp = icmp || proto == "icmp" || proto == "icmpv6" || proto == "58"
	}
	return ssh, icmp
}

// allow lets the protocol in from the networks; a rule that is there already is no error. EC2 takes
// ICMP from IPv6 networks only as icmpv6, so those go into a permission of their own.
func allow(ctx context.Context, a *apis, id, name, proto string, port int32, what string, sources []string) error {
	v4 := types.IpPermission{IpProtocol: ptr(proto), FromPort: ptr(port), ToPort: ptr(port)}
	v6 := types.IpPermission{IpProtocol: ptr(proto), FromPort: ptr(port), ToPort: ptr(port)}
	if proto == "icmp" {
		v6.IpProtocol = ptr("icmpv6")
	}
	for _, cidr := range sources {
		cidr = strings.TrimSpace(cidr)
		switch {
		case cidr == "":
		case strings.Contains(cidr, ":"):
			v6.Ipv6Ranges = append(v6.Ipv6Ranges, types.Ipv6Range{CidrIpv6: ptr(cidr)})
		default:
			v4.IpRanges = append(v4.IpRanges, types.IpRange{CidrIp: ptr(cidr)})
		}
	}
	var perms []types.IpPermission
	if proto == "icmp" {
		if len(v4.IpRanges) > 0 {
			perms = append(perms, v4)
		}
		if len(v6.Ipv6Ranges) > 0 {
			perms = append(perms, v6)
		}
	} else {
		v4.Ipv6Ranges = v6.Ipv6Ranges
		perms = append(perms, v4)
	}
	_, err := a.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{GroupId: ptr(id), IpPermissions: perms})
	if err != nil && apiCode(err) != "InvalidPermission.Duplicate" {
		return &cloud.OpError{Op: fmt.Sprintf("adding the %s rule to security group %s", what, name), Write: true, Err: err}
	}
	return nil
}
