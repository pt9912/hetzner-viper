package aws_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// fakeAWS is an EC2 region with state: what the adapter creates is there for the next call.
type fakeAWS struct {
	instances map[string]*types.Instance
	subnets   []types.Subnet
	groups    map[string]*group
	keys      []types.KeyPairInfo
	itypes    map[string]types.InstanceTypeInfo
	images    map[string]types.Image
	params    map[string]string

	runs          []*ec2.RunInstancesInput
	dryRunDenied  bool
	authorized    []*ec2.AuthorizeSecurityGroupIngressInput
	imported      []*ec2.ImportKeyPairInput
	created       []*ec2.CreateSecurityGroupInput
	terminated    []string
	modified      []*ec2.ModifyInstanceAttributeInput
	deleted       []string
	busyDeletes   int // DeleteSecurityGroup says DependencyViolation this many times first
	paramsAsked   []string
	subnetFilters [][]types.Filter
	describes     map[string]int // looks at an instance by ID, to let it take its time
	listErr       error
	nextID        int
}

type group struct {
	name, vpc string
	perms     []types.IpPermission
	tags      []types.Tag
}

func apiError(code, msg string) error { return &smithy.GenericAPIError{Code: code, Message: msg} }

func newFakeAWS() *fakeAWS {
	sub := func(id, az string) types.Subnet {
		return types.Subnet{SubnetId: ptr(id), VpcId: ptr("vpc-1"), AvailabilityZone: ptr(az)}
	}
	return &fakeAWS{
		instances: map[string]*types.Instance{},
		subnets:   []types.Subnet{sub("subnet-c", "eu-central-1c"), sub("subnet-a", "eu-central-1a"), sub("subnet-b", "eu-central-1b")},
		groups:    map[string]*group{},
		itypes: map[string]types.InstanceTypeInfo{
			"t3.large":  {VCpuInfo: &types.VCpuInfo{DefaultVCpus: ptr(int32(2))}, MemoryInfo: &types.MemoryInfo{SizeInMiB: ptr(int64(8192))}, ProcessorInfo: &types.ProcessorInfo{SupportedArchitectures: []types.ArchitectureType{types.ArchitectureTypeX8664}}},
			"t4g.small": {VCpuInfo: &types.VCpuInfo{DefaultVCpus: ptr(int32(2))}, MemoryInfo: &types.MemoryInfo{SizeInMiB: ptr(int64(2048))}, ProcessorInfo: &types.ProcessorInfo{SupportedArchitectures: []types.ArchitectureType{types.ArchitectureTypeArm64}}},
		},
		images: map[string]types.Image{
			"ami-0123456789abcdef0": {ImageId: ptr("ami-0123456789abcdef0"), RootDeviceName: ptr("/dev/sda1"), Architecture: types.ArchitectureValuesX8664},
			"ami-0fedcba987654321a": {ImageId: ptr("ami-0fedcba987654321a"), RootDeviceName: ptr("/dev/sda1"), Architecture: types.ArchitectureValuesArm64},
		},
		params: map[string]string{
			"/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id": "ami-0123456789abcdef0",
			"/aws/service/canonical/ubuntu/server/24.04/stable/current/arm64/hvm/ebs-gp3/ami-id": "ami-0fedcba987654321a",
		},
		describes: map[string]int{},
	}
}

func ptr[T any](v T) *T { return &v }

func tagOf(i *types.Instance, key string) string {
	for _, t := range i.Tags {
		if *t.Key == key {
			return *t.Value
		}
	}
	return ""
}

func hasFilter(fs []types.Filter, name, value string) bool {
	for _, f := range fs {
		if *f.Name == name {
			for _, v := range f.Values {
				if v == value {
					return true
				}
			}
			return false
		}
	}
	return true // no such filter: everything passes
}

func (f *fakeAWS) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var ids []string
	for id := range f.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []types.Instance
	for _, id := range ids {
		i := f.instances[id]
		if len(in.InstanceIds) > 0 {
			found := false
			for _, want := range in.InstanceIds {
				found = found || want == id
			}
			if !found {
				continue
			}
			f.describes[id]++
			// a new instance is pending for its first look, then it runs with an address
			if i.State.Name == types.InstanceStateNamePending && f.describes[id] >= 2 {
				i.State.Name = types.InstanceStateNameRunning
				i.PublicIpAddress = ptr("203.0.113.9")
				i.PublicDnsName = ptr("ec2-203-0-113-9.eu-central-1.compute.amazonaws.com")
			}
			// a terminated instance is shutting down for one look, then it is gone
			defer func() {
				if i.State.Name == types.InstanceStateNameShuttingDown {
					i.State.Name = types.InstanceStateNameTerminated
				}
			}()
		}
		if !tagFilters(in.Filters, i.Tags) || !hasFilter(in.Filters, "instance-state-name", string(i.State.Name)) {
			continue
		}
		out = append(out, *i)
	}
	return &ec2.DescribeInstancesOutput{Reservations: []types.Reservation{{Instances: out}}}, nil
}

// tagFilters applies the filters named tag:KEY to the tags.
func tagFilters(fs []types.Filter, tags []types.Tag) bool {
	for _, f := range fs {
		key, ok := strings.CutPrefix(*f.Name, "tag:")
		if !ok {
			continue
		}
		found := false
		for _, t := range tags {
			for _, v := range f.Values {
				found = found || (*t.Key == key && *t.Value == v)
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (f *fakeAWS) ModifyInstanceAttribute(_ context.Context, in *ec2.ModifyInstanceAttributeInput, _ ...func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error) {
	f.modified = append(f.modified, in)
	i, ok := f.instances[*in.InstanceId]
	if !ok {
		return nil, apiError("InvalidInstanceID.NotFound", "no such instance")
	}
	i.SecurityGroups = f.identifiers(in.Groups)
	return &ec2.ModifyInstanceAttributeOutput{}, nil
}

func (f *fakeAWS) identifiers(ids []string) []types.GroupIdentifier {
	var out []types.GroupIdentifier
	for _, id := range ids {
		name := ""
		if g := f.groups[id]; g != nil {
			name = g.name
		}
		out = append(out, types.GroupIdentifier{GroupId: ptr(id), GroupName: ptr(name)})
	}
	return out
}

func (f *fakeAWS) CreateTags(_ context.Context, in *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	for _, id := range in.Resources {
		i, ok := f.instances[id]
		if !ok {
			return nil, apiError("InvalidInstanceID.NotFound", "no such instance")
		}
		for _, t := range in.Tags {
			replaced := false
			for k := range i.Tags {
				if *i.Tags[k].Key == *t.Key {
					i.Tags[k], replaced = t, true
				}
			}
			if !replaced {
				i.Tags = append(i.Tags, t)
			}
		}
	}
	return &ec2.CreateTagsOutput{}, nil
}

func (f *fakeAWS) DeleteSecurityGroup(_ context.Context, in *ec2.DeleteSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	id := *in.GroupId
	if _, ok := f.groups[id]; !ok {
		return nil, apiError("InvalidGroup.NotFound", "no such group")
	}
	if f.busyDeletes > 0 {
		f.busyDeletes--
		return nil, apiError("DependencyViolation", "resource sg has a dependent object")
	}
	for _, i := range f.instances {
		if i.State.Name == types.InstanceStateNameTerminated {
			continue
		}
		for _, g := range i.SecurityGroups {
			if *g.GroupId == id {
				return nil, apiError("DependencyViolation", "resource sg has a dependent object")
			}
		}
	}
	delete(f.groups, id)
	f.deleted = append(f.deleted, id)
	return &ec2.DeleteSecurityGroupOutput{}, nil
}

func (f *fakeAWS) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	f.runs = append(f.runs, in)
	f.nextID++
	id := fmt.Sprintf("i-%04d", f.nextID)
	inst := &types.Instance{InstanceId: ptr(id), State: &types.InstanceState{Name: types.InstanceStateNamePending}, LaunchTime: ptr(time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC))}
	for _, ts := range in.TagSpecifications {
		if ts.ResourceType == types.ResourceTypeInstance {
			inst.Tags = ts.Tags
		}
	}
	inst.PrivateIpAddress = ptr(fmt.Sprintf("10.0.0.%d", f.nextID+10))
	for _, ni := range in.NetworkInterfaces {
		inst.SecurityGroups = f.identifiers(ni.Groups)
	}
	f.instances[id] = inst
	return &ec2.RunInstancesOutput{Instances: []types.Instance{*inst}}, nil
}

func (f *fakeAWS) TerminateInstances(_ context.Context, in *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	for _, id := range in.InstanceIds {
		i, ok := f.instances[id]
		if !ok {
			return nil, apiError("InvalidInstanceID.NotFound", "no such instance")
		}
		f.terminated = append(f.terminated, id)
		i.State.Name = types.InstanceStateNameShuttingDown
	}
	return &ec2.TerminateInstancesOutput{}, nil
}

func (f *fakeAWS) DescribeInstanceTypes(_ context.Context, in *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	t, ok := f.itypes[string(in.InstanceTypes[0])]
	if !ok {
		return nil, apiError("InvalidInstanceType", "The following supplied instance types do not exist")
	}
	return &ec2.DescribeInstanceTypesOutput{InstanceTypes: []types.InstanceTypeInfo{t}}, nil
}

func (f *fakeAWS) DescribeImages(_ context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	img, ok := f.images[in.ImageIds[0]]
	if !ok {
		return nil, apiError("InvalidAMIID.NotFound", "no such image")
	}
	return &ec2.DescribeImagesOutput{Images: []types.Image{img}}, nil
}

func (f *fakeAWS) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	f.subnetFilters = append(f.subnetFilters, in.Filters)
	var out []types.Subnet
	for _, s := range f.subnets {
		if len(in.SubnetIds) > 0 && *s.SubnetId != in.SubnetIds[0] {
			continue
		}
		if hasFilter(in.Filters, "availability-zone", *s.AvailabilityZone) && hasFilter(in.Filters, "vpc-id", *s.VpcId) {
			out = append(out, s)
		}
	}
	return &ec2.DescribeSubnetsOutput{Subnets: out}, nil
}

func (f *fakeAWS) DescribeSecurityGroups(_ context.Context, in *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	var out []types.SecurityGroup
	var ids []string
	for id := range f.groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := f.groups[id]
		if len(in.GroupIds) > 0 && !contains(in.GroupIds, id) {
			continue
		}
		if hasFilter(in.Filters, "group-name", g.name) && hasFilter(in.Filters, "vpc-id", g.vpc) && tagFilters(in.Filters, g.tags) {
			out = append(out, types.SecurityGroup{GroupId: ptr(id), GroupName: ptr(g.name), VpcId: ptr(g.vpc), IpPermissions: g.perms, Tags: g.tags})
		}
	}
	if len(in.GroupIds) > 0 && len(out) == 0 {
		return nil, apiError("InvalidGroup.NotFound", "no such group")
	}
	return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: out}, nil
}

func (f *fakeAWS) CreateSecurityGroup(_ context.Context, in *ec2.CreateSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	if in.DryRun != nil && *in.DryRun { // EC2: "yes" is an error too
		if f.dryRunDenied {
			return nil, apiError("UnauthorizedOperation", "not allowed")
		}
		return nil, apiError("DryRunOperation", "Request would have succeeded")
	}
	f.created = append(f.created, in)
	for _, g := range f.groups {
		if g.name == *in.GroupName && g.vpc == *in.VpcId {
			return nil, apiError("InvalidGroup.Duplicate", "the security group already exists")
		}
	}
	id := fmt.Sprintf("sg-%04d", len(f.groups)+len(f.deleted)+1)
	var tags []types.Tag
	for _, ts := range in.TagSpecifications {
		tags = append(tags, ts.Tags...)
	}
	f.groups[id] = &group{name: *in.GroupName, vpc: *in.VpcId, tags: tags}
	return &ec2.CreateSecurityGroupOutput{GroupId: ptr(id)}, nil
}

func (f *fakeAWS) AuthorizeSecurityGroupIngress(_ context.Context, in *ec2.AuthorizeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	f.authorized = append(f.authorized, in)
	g := f.groups[*in.GroupId]
	for _, p := range in.IpPermissions { // the same rule twice is refused, as EC2 does
		for _, have := range g.perms {
			if len(p.UserIdGroupPairs) > 0 && permKey(p) == permKey(have) {
				return nil, apiError("InvalidPermission.Duplicate", "the rule exists")
			}
		}
	}
	g.perms = append(g.perms, in.IpPermissions...)
	return &ec2.AuthorizeSecurityGroupIngressOutput{}, nil
}

func (f *fakeAWS) DescribeKeyPairs(context.Context, *ec2.DescribeKeyPairsInput, ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error) {
	return &ec2.DescribeKeyPairsOutput{KeyPairs: f.keys}, nil
}

func (f *fakeAWS) ImportKeyPair(_ context.Context, in *ec2.ImportKeyPairInput, _ ...func(*ec2.Options)) (*ec2.ImportKeyPairOutput, error) {
	f.imported = append(f.imported, in)
	return &ec2.ImportKeyPairOutput{}, nil
}

func (f *fakeAWS) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.paramsAsked = append(f.paramsAsked, *in.Name)
	v, ok := f.params[*in.Name]
	if !ok {
		return nil, apiError("ParameterNotFound", "no such parameter")
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: ptr(v)}}, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func permKey(p types.IpPermission) string {
	return fmt.Sprint(*p.IpProtocol, *p.FromPort, *p.ToPort, len(p.UserIdGroupPairs))
}
