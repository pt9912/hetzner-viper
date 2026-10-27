package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// userDataLimit is what EC2 takes as user data (before the base64 encoding it is 16 KB).
const userDataLimit = 16 * 1024

func ptr[T any](v T) *T { return &v }

// apiCode is the error code of an API error, "" for any other error.
func apiCode(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

func serverFrom(i types.Instance, name string) *model.Server {
	s := &model.Server{ID: str(i.InstanceId), Name: name, IPv4: str(i.PublicIpAddress)}
	if i.LaunchTime != nil {
		s.Created = *i.LaunchTime
	}
	return s
}

// ServerByName returns the instance tagged viper=<name> that is not on its way out, nil if there
// is none.
func (c *Client) ServerByName(ctx context.Context, name string) (*model.Server, error) {
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	out, err := a.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []types.Filter{
		filter("tag:viper", name),
		filter("instance-state-name", "pending", "running", "stopping", "stopped"),
	}})
	if err != nil {
		return nil, err
	}
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			return serverFrom(i, name), nil
		}
	}
	return nil, nil
}

// launch is what a new instance is made from, looked up before anything is created.
type launch struct {
	image  *image
	subnet string
	group  string
}

// plan looks up the instance type, the image, the subnet and the security group, and fails
// before anything is created if one of them is missing.
func (c *Client) plan(ctx context.Context, s model.ServerSpec) (*launch, error) {
	it, err := c.instanceType(ctx, s.Size)
	if err != nil {
		return nil, err
	}
	if it == nil {
		return nil, fmt.Errorf("instance type %s not found", s.Size)
	}
	img, err := c.image(ctx, s.Image, it.arch)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("image %s not found for %s", s.Image, it.arch)
	}
	n, err := c.network(ctx)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, fmt.Errorf("no default subnet: set AWS_SUBNET_ID")
	}
	sg, _, err := c.securityGroup(ctx, s.Firewall, n.vpc)
	if err != nil {
		return nil, err
	}
	if sg == "" {
		return nil, fmt.Errorf("security group %s not found", s.Firewall)
	}
	if len(s.UserData) > userDataLimit {
		return nil, fmt.Errorf("the cloud-init user data is %d bytes; EC2 takes %d", len(s.UserData), userDataLimit)
	}
	return &launch{image: img, subnet: n.subnet, group: sg}, nil
}

// CreateServer starts the instance and waits until it runs and has a public address.
func (c *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	l, err := c.plan(ctx, s)
	if err != nil {
		return nil, err
	}
	res, err := a.ec2.RunInstances(ctx, c.runInput(s, l))
	if err != nil {
		return nil, err
	}
	if len(res.Instances) == 0 {
		return nil, fmt.Errorf("EC2 started no instance")
	}
	id := str(res.Instances[0].InstanceId)
	var up types.Instance
	err = c.wait(ctx, "the instance to run", func() (bool, error) {
		i, err := instance(ctx, a, id)
		if err != nil || i == nil {
			return false, err
		}
		up = *i
		return i.State != nil && i.State.Name == types.InstanceStateNameRunning && i.PublicIpAddress != nil, nil
	})
	if err != nil {
		return nil, err
	}
	return serverFrom(up, s.Name), nil
}

func (c *Client) runInput(s model.ServerSpec, l *launch) *ec2.RunInstancesInput {
	return &ec2.RunInstancesInput{
		ImageId: ptr(l.image.id), InstanceType: types.InstanceType(s.Size), MinCount: ptr(int32(1)), MaxCount: ptr(int32(1)),
		KeyName:  ptr(s.SSHKey),
		UserData: ptr(base64.StdEncoding.EncodeToString([]byte(s.UserData))),
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex: ptr(int32(0)), SubnetId: ptr(l.subnet), AssociatePublicIpAddress: ptr(true), Groups: []string{l.group},
		}},
		BlockDeviceMappings: []types.BlockDeviceMapping{{
			DeviceName: ptr(l.image.rootDevice),
			Ebs:        &types.EbsBlockDevice{VolumeSize: ptr(c.opts.rootGB()), VolumeType: types.VolumeTypeGp3, DeleteOnTermination: ptr(true)},
		}},
		// the instance metadata only with a session token: a server-side request forgery cannot read it
		MetadataOptions: &types.InstanceMetadataOptionsRequest{HttpTokens: types.HttpTokensStateRequired, HttpEndpoint: types.InstanceMetadataEndpointStateEnabled},
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: tags(s.Name)},
			{ResourceType: types.ResourceTypeVolume, Tags: tags(s.Name)},
		},
	}
}

// instance looks at one instance, nil if EC2 does not list it.
func instance(ctx context.Context, a *apis, id string) (*types.Instance, error) {
	out, err := a.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		return nil, err
	}
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			return &i, nil
		}
	}
	return nil, nil
}

func (o Options) rootGB() int32 {
	if o.RootGB > 0 {
		return o.RootGB
	}
	return 50
}

// DeleteServer terminates the instance and waits until it is gone.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	a, err := c.api(ctx)
	if err != nil {
		return err
	}
	if _, err := a.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
		return err
	}
	return c.wait(ctx, "the instance to end", func() (bool, error) {
		i, err := instance(ctx, a, id)
		if err != nil {
			return false, err
		}
		return i == nil || (i.State != nil && i.State.Name == types.InstanceStateNameTerminated), nil
	})
}
