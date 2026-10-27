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
	sort.Slice(out.Subnets, func(i, j int) bool {
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

// hasRules reports whether the permissions let SSH and ping in (from anywhere).
func hasRules(perms []types.IpPermission) (ssh, icmp bool) {
	for _, p := range perms {
		proto := str(p.IpProtocol)
		ssh = ssh || (proto == "tcp" && p.FromPort != nil && *p.FromPort == 22 && p.ToPort != nil && *p.ToPort == 22)
		icmp = icmp || proto == "icmp"
	}
	return ssh, icmp
}

// allow lets the protocol in from the networks; a rule that is there already is no error.
func allow(ctx context.Context, a *apis, id, name, proto string, port int32, what string, sources []string) error {
	perm := types.IpPermission{IpProtocol: ptr(proto), FromPort: ptr(port), ToPort: ptr(port)}
	for _, cidr := range sources {
		cidr = strings.TrimSpace(cidr)
		if strings.Contains(cidr, ":") {
			perm.Ipv6Ranges = append(perm.Ipv6Ranges, types.Ipv6Range{CidrIpv6: ptr(cidr)})
		} else {
			perm.IpRanges = append(perm.IpRanges, types.IpRange{CidrIp: ptr(cidr)})
		}
	}
	_, err := a.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{GroupId: ptr(id), IpPermissions: []types.IpPermission{perm}})
	if err != nil && apiCode(err) != "InvalidPermission.Duplicate" {
		return &cloud.OpError{Op: fmt.Sprintf("adding the %s rule to security group %s", what, name), Write: true, Err: err}
	}
	return nil
}
