package aws

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

var (
	ubuntuRe = regexp.MustCompile(`^ubuntu-(\d\d\.\d\d)$`)
	amiRe    = regexp.MustCompile(`^ami-[0-9a-f]+$`)
)

// instanceType is what viper shows and decides by of an instance type.
type instanceType struct {
	name   string
	vcpus  int32
	memMiB int64
	arch   string // "arm64" or "x86_64"
}

// instanceType reads the instance type; nil if it does not exist in the region.
func (c *Client) instanceType(ctx context.Context, name string) (*instanceType, error) {
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	out, err := a.ec2.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{InstanceTypes: []types.InstanceType{types.InstanceType(name)}})
	if err != nil {
		if apiCode(err) == "InvalidInstanceType" {
			return nil, nil
		}
		return nil, err
	}
	if len(out.InstanceTypes) == 0 {
		return nil, nil
	}
	t := out.InstanceTypes[0]
	it := &instanceType{name: name, arch: "x86_64"}
	if t.VCpuInfo != nil && t.VCpuInfo.DefaultVCpus != nil {
		it.vcpus = *t.VCpuInfo.DefaultVCpus
	}
	if t.MemoryInfo != nil && t.MemoryInfo.SizeInMiB != nil {
		it.memMiB = *t.MemoryInfo.SizeInMiB
	}
	if t.ProcessorInfo != nil {
		for _, a := range t.ProcessorInfo.SupportedArchitectures {
			if a == types.ArchitectureTypeArm64 {
				it.arch = "arm64"
			}
		}
	}
	return it, nil
}

// image is an AMI as the server is made from it.
type image struct {
	id         string
	rootDevice string
}

// image resolves the setting SERVER_IMAGE: "ubuntu-24.04" is the AMI Canonical publishes for the
// architecture, "ami-…" is that AMI itself. nil if there is no such image.
func (c *Client) image(ctx context.Context, setting, arch string) (*image, error) {
	a, err := c.api(ctx)
	if err != nil {
		return nil, err
	}
	id, err := amiID(ctx, a, setting, arch)
	if err != nil || id == "" {
		return nil, err
	}
	out, err := a.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{id}})
	if err != nil {
		if strings.HasPrefix(apiCode(err), "InvalidAMIID") {
			return nil, nil
		}
		return nil, err
	}
	if len(out.Images) == 0 {
		return nil, nil
	}
	img := out.Images[0]
	if got := string(img.Architecture); got != arch {
		return nil, fmt.Errorf("image %s is for %s, the instance type needs %s", id, got, arch)
	}
	return &image{id: id, rootDevice: str(img.RootDeviceName)}, nil
}

// amiID is the AMI the setting names, "" if Canonical publishes none of that version.
func amiID(ctx context.Context, a *apis, setting, arch string) (string, error) {
	m := ubuntuRe.FindStringSubmatch(setting)
	if m == nil {
		if !amiRe.MatchString(setting) {
			return "", fmt.Errorf("image %s is not supported: use ubuntu-<version>, e.g. ubuntu-24.04, or an AMI ID (ami-…)", setting)
		}
		return setting, nil
	}
	cpu := "amd64"
	if arch == "arm64" {
		cpu = "arm64"
	}
	p, err := a.ssm.GetParameter(ctx, &ssm.GetParameterInput{Name: ptr(fmt.Sprintf("/aws/service/canonical/ubuntu/server/%s/stable/current/%s/hvm/ebs-gp3/ami-id", m[1], cpu))})
	if err != nil {
		if apiCode(err) == "ParameterNotFound" {
			return "", nil
		}
		return "", err
	}
	return str(p.Parameter.Value), nil
}
