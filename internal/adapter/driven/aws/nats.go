package aws

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NATS groups on AWS (docs/plan/nats-anbieter.md §3.2). The "private network" of a group is the VPC of
// the subnet the servers go to, plus the security group viper-nats-<group>: it lets the NATS port in
// from the group itself (a rule that names the group as its source) and nothing else. Creating that
// group is the lock of the first start: a name is unique in a VPC, so a second create is refused with
// InvalidGroup.Duplicate and only the first caller becomes the server candidate. No VPC or subnet is
// made or changed. The NATS labels are tags of the instance.

var _ cloud.NATSProvider = (*Client)(nil)

// tagCreated is the tag with the creation time of the security group of a group (it has none of its own).
const tagCreated = "viper-created"

// GroupName is the name of the security group of a NATS group.
func GroupName(group string) string { return "viper-nats-" + group }

func (o Options) natsPort() int32 {
	if o.NATSPort > 0 {
		return int32(o.NATSPort)
	}
	return int32(model.DefaultNATSPort)
}

// labelTags are the labels of a server as tags.
func labelTags(labels map[string]string) []types.Tag {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]types.Tag, 0, len(keys))
	for _, k := range keys {
		out = append(out, types.Tag{Key: ptr(k), Value: ptr(labels[k])})
	}
	return out
}

// groups are the security groups of a new instance: the one for SSH and, in a NATS group, the one of the group.
func groups(base, network string) []string {
	if network == "" {
		return []string{base}
	}
	return []string{base, network}
}

func tagMap(tags []types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[str(t.Key)] = str(t.Value)
	}
	return m
}

// inNATSGroup tells whether the instance has the security group of a NATS group.
func inNATSGroup(i types.Instance) bool {
	for _, g := range i.SecurityGroups {
		if len(str(g.GroupName)) > len("viper-nats-") && str(g.GroupName)[:len("viper-nats-")] == "viper-nats-" {
			return true
		}
	}
	return false
}

// NetworkZone is the region of the location (the servers of a group may be in different zones of a region).
func (c *Client) NetworkZone(_ context.Context, location string) (string, error) {
	o := c.opts
	o.Location = location
	if o.region() == "" {
		return "", fmt.Errorf("no region in the location %q", location)
	}
	return o.region(), nil
}

// ours tells whether viper made the security group for the group.
func ours(g types.SecurityGroup, group string) bool {
	t := tagMap(g.Tags)
	return t[model.LabelManaged] == "true" && t[model.LabelNATSGroup] == group
}

func networkOf(g types.SecurityGroup, region, cidr string) *model.PrivateNetwork {
	n := &model.PrivateNetwork{ID: str(g.GroupId), Name: str(g.GroupName), IPRange: cidr, Zone: region}
	if t, err := time.Parse(time.RFC3339, tagMap(g.Tags)[tagCreated]); err == nil {
		n.Created = t
	}
	return n
}

// EnsurePrivateNetwork creates the security group of the group, or finds it; see cloud.NATSProvider.
func (c *Client) EnsurePrivateNetwork(ctx context.Context, group, _ string) (model.PrivateNetwork, bool, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	a, err := c.api(ctx)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	n, err := c.network(ctx)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if n == nil {
		return model.PrivateNetwork{}, false, fmt.Errorf("no default subnet: set AWS_SUBNET_ID")
	}
	if err := sameVPC(ctx, a, group, n.vpc); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	name := GroupName(group)
	shared, err := awsTags("AWS_TAGS_SHARED", c.opts.SharedTags)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	own := append([]types.Tag{
		{Key: ptr("Name"), Value: ptr(name)}, {Key: ptr(model.LabelManaged), Value: ptr("true")},
		{Key: ptr(model.LabelNATSGroup), Value: ptr(group)}, {Key: ptr(tagCreated), Value: ptr(now.Format(time.RFC3339))},
	}, shared...)
	out, err := a.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: ptr(name), Description: ptr("viper: NATS group " + group), VpcId: ptr(n.vpc),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeSecurityGroup, Tags: own}},
	})
	created := err == nil
	var g types.SecurityGroup
	switch {
	case created:
		g = types.SecurityGroup{GroupId: out.GroupId, GroupName: ptr(name), Tags: own}
	case apiCode(err) == "InvalidGroup.Duplicate":
		// somebody was first: use the group, if it is the group's, in this VPC
		if g, err = findGroup(ctx, a, name, n.vpc); err != nil {
			return model.PrivateNetwork{}, false, err
		}
		if !ours(g, group) {
			return model.PrivateNetwork{}, false, fmt.Errorf("a security group named %s exists that viper did not create for the group %s", name, group)
		}
	default:
		return model.PrivateNetwork{}, false, &cloud.OpError{Op: fmt.Sprintf("creating the security group %s", name), Write: true, Err: err}
	}
	// the rule is made by whoever comes: it is not an error if it is there, and a first start that stopped
	// between the two calls is repaired by the next
	if err := c.allowGroup(ctx, a, str(g.GroupId), name); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	return *networkOf(g, c.opts.region(), n.cidr), created, nil
}

// allowGroup lets the NATS port in from the security group itself.
func (c *Client) allowGroup(ctx context.Context, a *apis, id, name string) error {
	port := c.opts.natsPort()
	_, err := a.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{GroupId: ptr(id), IpPermissions: []types.IpPermission{{
		IpProtocol: ptr("tcp"), FromPort: ptr(port), ToPort: ptr(port),
		UserIdGroupPairs: []types.UserIdGroupPair{{GroupId: ptr(id), Description: ptr("viper: NATS from the group")}},
	}}})
	if err != nil && apiCode(err) != "InvalidPermission.Duplicate" {
		return &cloud.OpError{Op: fmt.Sprintf("adding the NATS rule (TCP %d) to security group %s", port, name), Write: true, Err: err}
	}
	return nil
}

// findGroup returns the security group of that name in the VPC.
func findGroup(ctx context.Context, a *apis, name, vpc string) (types.SecurityGroup, error) {
	out, err := a.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: []types.Filter{filter("group-name", name), filter("vpc-id", vpc)}})
	if err != nil {
		return types.SecurityGroup{}, &cloud.OpError{Op: "listing the security groups", Err: err}
	}
	if len(out.SecurityGroups) == 0 {
		return types.SecurityGroup{}, fmt.Errorf("the security group %s was refused as existing but cannot be found: try again", name)
	}
	return out.SecurityGroups[0], nil
}

// sameVPC refuses a group whose security group is in another VPC: names are unique per VPC only, so
// two VPCs would each get a first start and a server.
func sameVPC(ctx context.Context, a *apis, group, vpc string) error {
	out, err := a.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: []types.Filter{
		filter("tag:"+model.LabelManaged, "true"), filter("tag:"+model.LabelNATSGroup, group),
	}})
	if err != nil {
		return &cloud.OpError{Op: "listing the security groups", Err: err}
	}
	for _, g := range out.SecurityGroups {
		if str(g.VpcId) != vpc {
			return fmt.Errorf("the NATS group %s exists in the VPC %s, this machine goes to %s: all machines of a group must be in one VPC (AWS_SUBNET_ID, AWS_VPC_ID)", group, str(g.VpcId), vpc)
		}
	}
	return nil
}

// FindPrivateNetwork returns the security group of the group, nil if there is none.
func (c *Client) FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	out, err := a.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: []types.Filter{
		filter("tag:"+model.LabelManaged, "true"), filter("tag:"+model.LabelNATSGroup, group),
	}})
	if err != nil {
		return nil, err
	}
	if len(out.SecurityGroups) == 0 {
		return nil, nil
	}
	cidr := ""
	if n, err := c.network(ctx); err == nil && n != nil {
		cidr = n.cidr
	}
	return networkOf(out.SecurityGroups[0], c.opts.region(), cidr), nil
}

// AttachPrivateNetwork adds the security group of the group to the instance and returns its private
// address. ModifyInstanceAttribute replaces the whole list, so the groups the instance has stay.
func (c *Client) AttachPrivateNetwork(ctx context.Context, serverID, networkID string) (string, error) {
	a, err := c.api(ctx)
	if err != nil {
		return "", err
	}
	i, err := instance(ctx, a, serverID)
	if err != nil {
		return "", err
	}
	if i == nil {
		return "", fmt.Errorf("instance %s not found", serverID)
	}
	ids := make([]string, 0, len(i.SecurityGroups)+1)
	for _, g := range i.SecurityGroups {
		if str(g.GroupId) == networkID {
			return str(i.PrivateIpAddress), nil // attached already
		}
		ids = append(ids, str(g.GroupId))
	}
	if _, err := a.ec2.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{InstanceId: ptr(serverID), Groups: append(ids, networkID)}); err != nil {
		return "", &cloud.OpError{Op: "adding the NATS security group to the instance", Write: true, Err: err}
	}
	return str(i.PrivateIpAddress), nil
}

// deleteTries is how often DeletePrivateNetwork tries while AWS still counts the network interface of an
// instance that just ended.
const deleteTries = 30

// DeletePrivateNetwork deletes the security group if viper made it; ErrNetworkInUse if instances still use it.
func (c *Client) DeletePrivateNetwork(ctx context.Context, networkID string) error {
	a, err := c.api(ctx)
	if err != nil {
		return err
	}
	out, err := a.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{networkID}})
	if apiCode(err) == "InvalidGroup.NotFound" || (err == nil && len(out.SecurityGroups) == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	g := out.SecurityGroups[0]
	if t := tagMap(g.Tags); t[model.LabelManaged] != "true" || t[model.LabelNATSGroup] == "" {
		return fmt.Errorf("the security group %s was not made by viper for a NATS group: not deleting it", networkID)
	}
	for try := 0; try < deleteTries; try++ {
		_, err = a.ec2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: ptr(networkID)})
		switch apiCode(err) {
		case "":
			return nil
		case "InvalidGroup.NotFound":
			return nil
		case "DependencyViolation":
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.opts.poll()):
			}
		default:
			return err
		}
	}
	return cloud.ErrNetworkInUse
}

// natsInstances are the instances of the group that are not on their way out.
func natsInstances(ctx context.Context, a *apis, group string) ([]types.Instance, error) {
	out, err := a.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []types.Filter{
		filter("tag:"+model.LabelManaged, "true"), filter("tag:"+model.LabelNATSGroup, group),
		filter("instance-state-name", "pending", "running", "stopping", "stopped"),
	}})
	if err != nil {
		return nil, err
	}
	var all []types.Instance
	for _, r := range out.Reservations {
		all = append(all, r.Instances...)
	}
	return all, nil
}

// ListGroupMachines returns the instances of the group with their NATS tags; an instance with tags that
// make no sense is an error, so nobody guesses about the group.
func (c *Client) ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	all, err := natsInstances(ctx, a, group)
	if err != nil {
		return nil, err
	}
	var out []model.GroupMember
	for _, i := range all {
		t := tagMap(i.Tags)
		meta, ok, err := model.NATSMetadataFromLabels(t)
		if err != nil {
			return nil, fmt.Errorf("instance %s: %w", str(i.InstanceId), err)
		}
		if !ok || meta.Group != group {
			continue
		}
		out = append(out, model.GroupMember{Server: *serverFrom(i, t["viper"]), NATS: meta})
	}
	return out, nil
}

// UpdateNATSMetadata sets the NATS tags of the instance; CreateTags writes the keys it is given and leaves the others.
func (c *Client) UpdateNATSMetadata(ctx context.Context, serverID string, meta model.NATSMetadata) error {
	if err := model.CheckNATSName("NATS group", meta.Group); err != nil {
		return err
	}
	a, err := c.api(ctx)
	if err != nil {
		return err
	}
	if _, err := a.ec2.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{serverID}, Tags: labelTags(model.NATSLabels(meta))}); err != nil {
		return &cloud.OpError{Op: "setting the NATS tags", Write: true, Err: err}
	}
	return nil
}

// NATSMetadataOf returns the NATS tags of the instance, nil if it has none.
func (c *Client) NATSMetadataOf(ctx context.Context, serverID string) (*model.NATSMetadata, error) {
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	i, err := instance(ctx, a, serverID)
	if err != nil {
		return nil, err
	}
	if i == nil {
		return nil, fmt.Errorf("instance %s not found", serverID)
	}
	meta, ok, err := model.NATSMetadataFromLabels(tagMap(i.Tags))
	if err != nil || !ok {
		return nil, err
	}
	return &meta, nil
}
