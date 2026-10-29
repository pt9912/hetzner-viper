// Package aws is the adapter of the cloud port (internal/port/driven/cloud) for Amazon EC2: a
// server is an instance in the default VPC (or the subnet of AWS_SUBNET_ID), the login key a key
// pair, the firewall a security group. What viper creates is tagged viper=<server name>;
// the instance and its volume also get the tags of AWS_TAGS.
package aws

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
)

// Instances is the part of the EC2 API that runs, finds and describes instances.
type Instances interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeInstanceTypes(ctx context.Context, in *ec2.DescribeInstanceTypesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// Networking is the part of the EC2 API for the subnets and security groups.
type Networking interface {
	DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(ctx context.Context, in *ec2.DescribeSecurityGroupsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	CreateSecurityGroup(ctx context.Context, in *ec2.CreateSecurityGroupInput, opts ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, in *ec2.AuthorizeSecurityGroupIngressInput, opts ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
}

// KeyPairs is the part of the EC2 API for the key pairs.
type KeyPairs interface {
	DescribeKeyPairs(ctx context.Context, in *ec2.DescribeKeyPairsInput, opts ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error)
	ImportKeyPair(ctx context.Context, in *ec2.ImportKeyPairInput, opts ...func(*ec2.Options)) (*ec2.ImportKeyPairOutput, error)
}

// EC2 is the part of the EC2 API the adapter uses.
type EC2 interface {
	Instances
	Networking
	KeyPairs
}

// SSM is the part of the Systems Manager API the adapter uses (the AMI IDs Canonical publishes).
type SSM interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// Options are the settings of the adapter.
type Options struct {
	// Location is SERVER_LOCATION: a region ("eu-central-1") or an availability zone in it
	// ("eu-central-1a").
	Location string
	Profile  string // AWS_PROFILE, "" for the default credentials chain
	SubnetID string // AWS_SUBNET_ID, "" for the default subnet
	VPCID    string // AWS_VPC_ID, "" for the default VPC
	RootGB   int32  // size of the root volume
	Tags     string // AWS_TAGS: KEY=VALUE pairs, comma-separated, for the instance and its volume
	// Poll is how long to wait between two looks at an instance (default 3 s).
	Poll time.Duration
}

// Client talks to EC2 with the credentials of the AWS credentials chain (aws login, SSO, keys).
type Client struct {
	opts Options

	mu   sync.Mutex
	apis *apis
	net  *network // the subnet the server goes to, once found
}

// New returns a client. It looks for the credentials when it is first used.
func New(opts Options) *Client { return &Client{opts: opts} }

// NewWith returns a client over given APIs (for tests).
func NewWith(opts Options, e EC2, s SSM) *Client {
	return &Client{opts: opts, apis: &apis{ec2: e, ssm: s}}
}

// Name is the short name of the provider in messages.
func (*Client) Name() string { return "AWS" }

// Title is the name of the provider in the preflight.
func (*Client) Title() string { return "AWS EC2" }

// Hint says what to check when the API cannot be used.
func (*Client) Hint() string { return "AWS credentials (aws login, AWS_PROFILE) / region / network" }

// Describe is an API error without the request details, safe to show.
func (*Client) Describe(err error) string {
	if err == nil {
		return ""
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode() + ": " + api.ErrorMessage()
	}
	msg := err.Error()
	if strings.Contains(msg, "failed to retrieve credentials") || strings.Contains(msg, "NoCredentialProviders") || strings.Contains(msg, "no EC2 IMDS role") {
		return "no valid AWS credentials (run aws login, or set AWS_PROFILE)"
	}
	return strings.TrimSpace(msg)
}

var zoneRe = regexp.MustCompile(`^([a-z]{2}(?:-[a-z]+)+-\d)([a-z])$`)

// region and zone of the location: "eu-central-1a" is zone a of eu-central-1.
func (o Options) region() string {
	if m := zoneRe.FindStringSubmatch(o.Location); m != nil {
		return m[1]
	}
	return o.Location
}

func (o Options) zone() string {
	if zoneRe.MatchString(o.Location) {
		return o.Location
	}
	return ""
}

func (o Options) poll() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 3 * time.Second
}

// apis are the clients of the services the adapter calls.
type apis struct {
	ec2 EC2
	ssm SSM
}

// api makes the SDK clients when they are first needed.
func (c *Client) api(ctx context.Context) (*apis, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apis != nil {
		return c.apis, nil
	}
	loaders := []func(*config.LoadOptions) error{config.WithRegion(c.opts.region())}
	if c.opts.Profile != "" {
		loaders = append(loaders, config.WithSharedConfigProfile(c.opts.Profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, loaders...)
	if err != nil {
		return nil, err
	}
	c.apis = &apis{ec2: ec2.NewFromConfig(cfg), ssm: ssm.NewFromConfig(cfg)}
	return c.apis, nil
}

// wait calls done until it reports true, looking every Poll, for at most five minutes.
func (c *Client) wait(ctx context.Context, what string, done func() (bool, error)) error {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.opts.poll()):
		}
	}
}

func str(s *string) string { return awssdk.ToString(s) }
