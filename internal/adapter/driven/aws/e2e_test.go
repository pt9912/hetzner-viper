//go:build e2e

package aws_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// TestE2E runs the adapter against a real AWS account: a key pair, a security group and one small
// instance, all named viper-e2e-<time> and removed at the end. It costs a few cents. Run it with
//
//	VIPER_E2E_AWS=<profile> go test -tags e2e -run TestE2E -v -timeout 15m ./internal/adapter/driven/aws/
func TestE2E(t *testing.T) {
	profile := os.Getenv("VIPER_E2E_AWS")
	if profile == "" {
		t.Skip("set VIPER_E2E_AWS=<profile> to run against a real account")
	}
	region := os.Getenv("VIPER_E2E_REGION")
	if region == "" {
		region = "eu-central-1"
	}
	ctx := context.Background()
	name := fmt.Sprintf("viper-e2e-%d", time.Now().Unix())
	c := aws.New(aws.Options{Location: region, Profile: profile, Poll: 3 * time.Second})

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region), awsconfig.WithSharedConfigProfile(profile))
	if err != nil {
		t.Fatal(err)
	}
	raw := ec2.NewFromConfig(cfg)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blob := binary.BigEndian.AppendUint32(nil, 11)
	blob = append(blob, "ssh-ed25519"...)
	blob = binary.BigEndian.AppendUint32(blob, uint32(len(pub)))
	blob = append(blob, pub...)
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " viper-e2e"

	// what the preflight says about this account
	for _, f := range c.Validate(ctx, model.Check{ServerName: name, KeyName: name, KeyFile: "/e2e", PublicKey: line,
		Spec: model.ServerSpec{Name: name, Size: "t3.micro", Region: region, Image: "ubuntu-24.04"}}) {
		t.Logf("preflight %d: %s", f.Level, f.Text)
		if f.Level == model.Fail {
			t.Errorf("preflight failed: %s", f.Text)
		}
	}

	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := raw.DeleteKeyPair(cctx, &ec2.DeleteKeyPairInput{KeyName: &name}); err != nil {
			t.Errorf("LEFT OVER key pair %s: %v", name, err)
		}
		sg, err := raw.DescribeSecurityGroups(cctx, &ec2.DescribeSecurityGroupsInput{GroupNames: []string{name}})
		if err == nil && len(sg.SecurityGroups) == 1 {
			if _, err := raw.DeleteSecurityGroup(cctx, &ec2.DeleteSecurityGroupInput{GroupId: sg.SecurityGroups[0].GroupId}); err != nil {
				t.Errorf("LEFT OVER security group %s: %v", name, err)
			}
		}
	})

	key, err := c.EnsureLoginKey(ctx, name, line)
	if err != nil || !key.Created || key.Ref != name {
		t.Fatalf("EnsureLoginKey: %+v %v", key, err)
	}
	key, err = c.EnsureLoginKey(ctx, name, line)
	if err != nil || key.Created || key.Ref != name {
		t.Fatalf("the second EnsureLoginKey must find the key by its fingerprint: %+v %v", key, err)
	}

	fw, err := c.EnsureFirewall(ctx, name, []string{"203.0.113.0/24"})
	if err != nil || !fw.Created || fw.Complete {
		t.Fatalf("EnsureFirewall: %+v %v", fw, err)
	}
	fw, err = c.EnsureFirewall(ctx, name, []string{"203.0.113.0/24"})
	if err != nil || fw.Created || !fw.Complete {
		t.Fatalf("the second EnsureFirewall must find both rules: %+v %v", fw, err)
	}

	userData := "#cloud-config\nruncmd:\n  - touch /var/tmp/viper-e2e\n"
	srv, err := c.CreateServer(ctx, model.ServerSpec{Name: name, Size: "t3.micro", Region: region, Image: "ubuntu-24.04", SSHKey: key.Ref, Firewall: name, UserData: userData})
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	t.Logf("server %s at %s", srv.ID, srv.IPv4)
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			if err := c.DeleteServer(context.Background(), srv.ID); err != nil {
				t.Errorf("LEFT OVER instance %s: %v", srv.ID, err)
			}
		}
	})
	if srv.ID == "" || !strings.HasPrefix(srv.ID, "i-") || srv.IPv4 == "" || srv.Created.IsZero() {
		t.Errorf("server %+v", srv)
	}
	got, err := c.ServerByName(ctx, name)
	if err != nil || got == nil || got.ID != srv.ID {
		t.Errorf("ServerByName: %+v %v", got, err)
	}

	if err := c.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	deleted = true
	if got, err := c.ServerByName(ctx, name); err != nil || got != nil {
		t.Errorf("after the delete: %+v %v", got, err)
	}
}
