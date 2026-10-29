package aws_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

func client(f *fakeAWS, location string) *aws.Client {
	return aws.NewWith(aws.Options{Location: location, Poll: 1}, f, f)
}

// edKey is an OpenSSH ED25519 public key line and the fingerprint EC2 gives it.
func edKey(seed byte) (line, fp string) {
	blob := binary.BigEndian.AppendUint32(nil, 11)
	blob = append(blob, "ssh-ed25519"...)
	blob = binary.BigEndian.AppendUint32(blob, 32)
	for i := 0; i < 32; i++ {
		blob = append(blob, seed+byte(i))
	}
	sum := sha256.Sum256(blob)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " me@laptop", "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func prepare(t *testing.T, f *fakeAWS, c *aws.Client) {
	t.Helper()
	if _, err := c.EnsureFirewall(context.Background(), "claude-ssh", []string{"0.0.0.0/0", "::/0"}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateServerAddsAWSTagsToInstanceAndVolume(t *testing.T) {
	f := newFakeAWS()
	c := aws.NewWith(aws.Options{Location: "eu-central-1", Tags: "owner=me, ttl=2026-11-01T12:00:00Z"}, f, f)
	prepare(t, f, c)
	if _, err := c.CreateServer(context.Background(), model.ServerSpec{
		Name: "claude-web", Size: "t4g.small", Region: "eu-central-1", Image: "ubuntu-24.04", SSHKey: "claude-login", Firewall: "claude-ssh",
	}); err != nil {
		t.Fatal(err)
	}
	for _, ts := range f.runs[0].TagSpecifications {
		got := map[string]string{}
		for _, tg := range ts.Tags {
			got[*tg.Key] = *tg.Value
		}
		if len(got) != 4 || got["Name"] != "claude-web" || got["viper"] != "claude-web" || got["owner"] != "me" || got["ttl"] != "2026-11-01T12:00:00Z" {
			t.Errorf("%s tags %v", ts.ResourceType, got)
		}
	}
}

func tagMap(tags []types.Tag) map[string]string {
	got := map[string]string{}
	for _, tg := range tags {
		got[*tg.Key] = *tg.Value
	}
	return got
}

func TestSharedTagsGoToTheKeyPairAndTheSecurityGroupOnly(t *testing.T) {
	line, _ := edKey(1)
	f := newFakeAWS()
	c := aws.NewWith(aws.Options{Location: "eu-central-1", Tags: "ttl=x", SharedTags: "project=p,managed-by=viper"}, f, f)
	prepare(t, f, c)
	if _, err := c.EnsureLoginKey(context.Background(), "claude-login", line); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "t4g.small", Image: "ubuntu-24.04", SSHKey: "claude-login", Firewall: "claude-ssh"}); err != nil {
		t.Fatal(err)
	}
	if got := tagMap(f.imported[0].TagSpecifications[0].Tags); len(got) != 3 || got["viper"] != "claude-login" || got["project"] != "p" || got["managed-by"] != "viper" {
		t.Errorf("key pair tags %v", got)
	}
	if got := tagMap(f.created[0].TagSpecifications[0].Tags); len(got) != 4 || got["Name"] != "claude-ssh" || got["project"] != "p" || got["ttl"] != "" {
		t.Errorf("security group tags %v", got)
	}
	for _, ts := range f.runs[0].TagSpecifications {
		if got := tagMap(ts.Tags); got["ttl"] != "x" || got["project"] != "" {
			t.Errorf("%s tags %v: AWS_TAGS only", ts.ResourceType, got)
		}
	}
}

func TestBadSharedTagsStopTheKeyPairAndTheSecurityGroup(t *testing.T) {
	line, _ := edKey(1)
	f := newFakeAWS()
	c := aws.NewWith(aws.Options{Location: "eu-central-1", SharedTags: "project"}, f, f)
	if _, err := c.EnsureLoginKey(context.Background(), "claude-login", line); err == nil || !strings.Contains(err.Error(), "AWS_TAGS_SHARED") || len(f.imported) != 0 {
		t.Errorf("key pair: %v", err)
	}
	if _, err := c.EnsureFirewall(context.Background(), "claude-ssh", []string{"0.0.0.0/0"}); err == nil || !strings.Contains(err.Error(), "AWS_TAGS_SHARED") || len(f.created) != 0 {
		t.Errorf("security group: %v", err)
	}
}

func TestCreateServerRefusesBadAWSTagsBeforeCreating(t *testing.T) {
	for _, tags := range []string{"novalue", "Name=x", "viper=x", "aws:x=1", "a=1,a=2", "k=" + strings.Repeat("v", 257), strings.Repeat("k", 129) + "=v"} {
		f := newFakeAWS()
		c := aws.NewWith(aws.Options{Location: "eu-central-1", Tags: tags}, f, f)
		prepare(t, f, c)
		_, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "t4g.small", Image: "ubuntu-24.04", SSHKey: "k", Firewall: "claude-ssh"})
		if err == nil || !strings.Contains(err.Error(), "AWS_TAGS") || len(f.runs) != 0 {
			t.Errorf("AWS_TAGS=%q: err %v, runs %d", tags, err, len(f.runs))
		}
	}
	var many []string
	for i := 0; i < 49; i++ {
		many = append(many, fmt.Sprintf("k%d=v", i))
	}
	f := newFakeAWS()
	c := aws.NewWith(aws.Options{Location: "eu-central-1", Tags: strings.Join(many, ",")}, f, f)
	prepare(t, f, c)
	if _, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "t4g.small", Image: "ubuntu-24.04", SSHKey: "k", Firewall: "claude-ssh"}); err == nil || len(f.runs) != 0 {
		t.Errorf("49 tags and viper's two are over 50: err %v", err)
	}
}

func TestCreateServerMakesTheInstanceAndWaitsForItsAddress(t *testing.T) {
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	prepare(t, f, c)
	srv, err := c.CreateServer(context.Background(), model.ServerSpec{
		Name: "claude-web", Size: "t4g.small", Region: "eu-central-1", Image: "ubuntu-24.04", SSHKey: "claude-login", Firewall: "claude-ssh", UserData: "#cloud-config\nx: 1\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Hostname != "ec2-203-0-113-9.eu-central-1.compute.amazonaws.com" {
		t.Errorf("hostname %q: the public name the cloud gives the instance", srv.Hostname)
	}
	if srv.ID != "i-0001" || srv.IPv4 != "203.0.113.9" || srv.Name != "claude-web" || srv.Created.IsZero() {
		t.Errorf("%+v", srv)
	}
	if len(f.paramsAsked) != 1 || !strings.Contains(f.paramsAsked[0], "/24.04/stable/current/arm64/") {
		t.Errorf("image parameter %v: an arm64 type needs the arm64 image", f.paramsAsked)
	}
	r := f.runs[0]
	ni := r.NetworkInterfaces[0]
	if *r.ImageId != "ami-0fedcba987654321a" || r.InstanceType != "t4g.small" || *r.KeyName != "claude-login" || *ni.SubnetId != "subnet-a" || !*ni.AssociatePublicIpAddress || len(ni.Groups) != 1 {
		t.Errorf("run %+v", r)
	}
	ud, _ := base64.StdEncoding.DecodeString(*r.UserData)
	if string(ud) != "#cloud-config\nx: 1\n" {
		t.Errorf("user data %q is not sent base64-encoded", ud)
	}
	if *r.BlockDeviceMappings[0].DeviceName != "/dev/sda1" || *r.BlockDeviceMappings[0].Ebs.VolumeSize != 50 {
		t.Errorf("root volume %+v", r.BlockDeviceMappings[0])
	}
	// viper down terminates the instance only: the volume must go with it, or it stays and costs money
	if d := r.BlockDeviceMappings[0].Ebs.DeleteOnTermination; d == nil || !*d {
		t.Error("the root volume must be deleted with the instance (DeleteOnTermination)")
	}
	if len(r.BlockDeviceMappings) != 1 {
		t.Errorf("%d block devices, want only the root volume", len(r.BlockDeviceMappings))
	}
	if r.MetadataOptions.HttpTokens != types.HttpTokensStateRequired {
		t.Error("the instance metadata must need a session token (IMDSv2)")
	}
	if len(r.TagSpecifications) != 2 || *r.TagSpecifications[0].Tags[1].Key != "viper" || *r.TagSpecifications[0].Tags[1].Value != "claude-web" {
		t.Errorf("tags %+v", r.TagSpecifications)
	}
	// the instance can be found again by name
	if got, err := c.ServerByName(context.Background(), "claude-web"); err != nil || got == nil || got.ID != "i-0001" {
		t.Errorf("ServerByName: %+v %v", got, err)
	}
}

func TestCreateServerTakesTheSubnetOfTheZone(t *testing.T) {
	f := newFakeAWS()
	c := client(f, "eu-central-1b")
	prepare(t, f, c)
	if _, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "x", Size: "t3.large", Image: "ubuntu-24.04", SSHKey: "k", Firewall: "claude-ssh"}); err != nil {
		t.Fatal(err)
	}
	if got := *f.runs[0].NetworkInterfaces[0].SubnetId; got != "subnet-b" {
		t.Errorf("subnet %s, want the one in eu-central-1b", got)
	}
	if *f.runs[0].ImageId != "ami-0123456789abcdef0" {
		t.Errorf("image %s", *f.runs[0].ImageId)
	}
}

func TestCreateServerFailsBeforeCreatingAnything(t *testing.T) {
	spec := model.ServerSpec{Name: "x", Size: "t3.large", Image: "ubuntu-24.04", SSHKey: "k", Firewall: "claude-ssh"}
	for name, mod := range map[string]func(*model.ServerSpec){
		"unknown type":  func(s *model.ServerSpec) { s.Size = "nope.huge" },
		"unknown image": func(s *model.ServerSpec) { s.Image = "ubuntu-99.99" },
		"bad image":     func(s *model.ServerSpec) { s.Image = "debian" },
		"no group":      func(s *model.ServerSpec) { s.Firewall = "missing" },
		"too much data": func(s *model.ServerSpec) { s.UserData = strings.Repeat("x", 16*1024+1) },
	} {
		f := newFakeAWS()
		c := client(f, "eu-central-1")
		prepare(t, f, c)
		s := spec
		mod(&s)
		if _, err := c.CreateServer(context.Background(), s); err == nil || len(f.runs) != 0 {
			t.Errorf("%s: err %v, runs %d", name, err, len(f.runs))
		}
	}
}

func TestAnAmiThatIsForAnotherArchitectureIsRefused(t *testing.T) {
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	prepare(t, f, c)
	_, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "x", Size: "t3.large", Image: "ami-0fedcba987654321a", SSHKey: "k", Firewall: "claude-ssh"})
	if err == nil || !strings.Contains(err.Error(), "needs x86_64") {
		t.Errorf("got %v", err)
	}
}

func TestServerByNameIgnoresInstancesThatAreGone(t *testing.T) {
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	prepare(t, f, c)
	f.instances["i-old"] = &types.Instance{InstanceId: ptr("i-old"), State: &types.InstanceState{Name: types.InstanceStateNameTerminated}, Tags: []types.Tag{{Key: ptr("viper"), Value: ptr("claude-web")}}}
	f.instances["i-other"] = &types.Instance{InstanceId: ptr("i-other"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, Tags: []types.Tag{{Key: ptr("viper"), Value: ptr("claude-api")}}}
	if got, err := c.ServerByName(context.Background(), "claude-web"); err != nil || got != nil {
		t.Errorf("got %+v %v: a terminated instance and one of another machine are not this server", got, err)
	}
}

func TestDeleteServerWaitsUntilTheInstanceIsTerminated(t *testing.T) {
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	prepare(t, f, c)
	srv, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "t3.large", Image: "ubuntu-24.04", SSHKey: "k", Firewall: "claude-ssh"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteServer(context.Background(), srv.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.terminated) != 1 || f.terminated[0] != srv.ID {
		t.Errorf("terminated %v", f.terminated)
	}
	if err := c.DeleteServer(context.Background(), "i-nope"); err == nil {
		t.Error("deleting an unknown instance must fail")
	}
}

func TestEnsureLoginKey(t *testing.T) {
	ctx := context.Background()
	line, fp := edKey(1)
	other, otherFP := edKey(50)

	f := newFakeAWS()
	c := client(f, "eu-central-1")
	key, err := c.EnsureLoginKey(ctx, "claude-login", line)
	if err != nil || key.Ref != "claude-login" || !key.Created || len(f.imported) != 1 || string(f.imported[0].PublicKeyMaterial) != line {
		t.Fatalf("import: %+v %v", key, err)
	}

	// the same key is in the region already, under another name: it is reused
	f.keys = []types.KeyPairInfo{{KeyName: ptr("my laptop"), KeyFingerprint: ptr(fp)}}
	if key, err = c.EnsureLoginKey(ctx, "claude-login", line); err != nil || key.Ref != "my laptop" || key.Created || len(f.imported) != 1 {
		t.Errorf("reuse: %+v %v", key, err)
	}

	// the name belongs to another key
	f.keys = []types.KeyPairInfo{{KeyName: ptr("claude-login"), KeyFingerprint: ptr(otherFP)}}
	var conflict *cloud.KeyConflictError
	if _, err = c.EnsureLoginKey(ctx, "claude-login", line); !errors.As(err, &conflict) || len(f.imported) != 1 {
		t.Errorf("conflict: %v", err)
	}
	_ = other
}

func TestAnImportedRSAKeyIsFoundByItsMD5Fingerprint(t *testing.T) {
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	blob := append(binary.BigEndian.AppendUint32(nil, 7), "ssh-rsa"...)
	blob = append(blob, 1, 2, 3, 4, 5, 6, 7, 8)
	line := "ssh-rsa " + base64.StdEncoding.EncodeToString(blob) + " me@laptop"
	f.keys = []types.KeyPairInfo{{KeyName: ptr("my rsa"), KeyFingerprint: ptr(model.FingerprintMD5(line))}}
	key, err := c.EnsureLoginKey(context.Background(), "claude-login", line)
	if err != nil || key.Ref != "my rsa" || key.Created || len(f.imported) != 0 {
		t.Errorf("%+v %v: EC2 gives RSA keys the MD5 fingerprint", key, err)
	}
}

func TestEnsureFirewall(t *testing.T) {
	ctx := context.Background()
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	res, err := c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8", "::/0"})
	if err != nil || !res.Created || res.Complete {
		t.Fatalf("first: %+v %v", res, err)
	}
	if len(f.created) != 1 || *f.created[0].VpcId != "vpc-1" || len(f.authorized) != 2 {
		t.Fatalf("created %d, authorized %d", len(f.created), len(f.authorized))
	}
	ssh, icmp := f.authorized[0].IpPermissions[0], f.authorized[1].IpPermissions[0]
	if *ssh.IpProtocol != "tcp" || *ssh.FromPort != 22 || *ssh.ToPort != 22 || *ssh.IpRanges[0].CidrIp != "10.0.0.0/8" || *ssh.Ipv6Ranges[0].CidrIpv6 != "::/0" {
		t.Errorf("ssh rule %+v", ssh)
	}
	if *icmp.IpProtocol != "icmp" || *icmp.FromPort != -1 || *icmp.ToPort != -1 || len(icmp.Ipv6Ranges) != 0 {
		t.Errorf("icmp rule %+v: all ICMP types are -1/-1, and IPv6 networks are not part of it", icmp)
	}
	icmp6 := f.authorized[1].IpPermissions[1]
	if *icmp6.IpProtocol != "icmpv6" || *icmp6.FromPort != -1 || *icmp6.Ipv6Ranges[0].CidrIpv6 != "::/0" || len(icmp6.IpRanges) != 0 {
		t.Errorf("icmpv6 rule %+v: EC2 takes ICMP from IPv6 networks only as icmpv6", icmp6)
	}

	// everything is there: nothing is added, and it says so
	res, err = c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8"})
	if err != nil || res.Created || !res.Complete || len(f.authorized) != 2 {
		t.Errorf("second: %+v %v, authorized %d", res, err, len(f.authorized))
	}

	// a group with SSH only gets the missing ping rule
	for _, g := range f.groups {
		g.perms = g.perms[:1]
	}
	res, err = c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8"})
	if err != nil || res.Complete || len(f.authorized) != 3 || *f.authorized[2].IpPermissions[0].IpProtocol != "icmp" {
		t.Errorf("third: %+v %v, authorized %d", res, err, len(f.authorized))
	}
}

func TestEnsureFirewallWithoutANetworkIsAnError(t *testing.T) {
	f := newFakeAWS()
	if _, err := client(f, "eu-central-1").EnsureFirewall(context.Background(), "claude-ssh", []string{" ", ""}); err == nil || len(f.created) != 0 {
		t.Errorf("got %v, created %d", err, len(f.created))
	}
}

func TestAPublicSubnetIsPreferred(t *testing.T) {
	f := newFakeAWS()
	f.subnets = []types.Subnet{
		{SubnetId: ptr("subnet-private"), VpcId: ptr("vpc-1"), AvailabilityZone: ptr("eu-central-1a"), MapPublicIpOnLaunch: ptr(false)},
		{SubnetId: ptr("subnet-public"), VpcId: ptr("vpc-1"), AvailabilityZone: ptr("eu-central-1b"), MapPublicIpOnLaunch: ptr(true)},
	}
	c := aws.NewWith(aws.Options{Location: "eu-central-1", VPCID: "vpc-1", Poll: 1}, f, f)
	if _, err := c.EnsureFirewall(context.Background(), "claude-ssh", []string{"10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "x", Size: "t3.large", Image: "ubuntu-24.04", SSHKey: "k", Firewall: "claude-ssh"}); err != nil {
		t.Fatal(err)
	}
	if got := *f.runs[0].NetworkInterfaces[0].SubnetId; got != "subnet-public" {
		t.Errorf("subnet %s: a server in a private subnet has no public address", got)
	}
}

func TestValidateFailsOnBadAWSTags(t *testing.T) {
	line, _ := edKey(1)
	f := newFakeAWS()
	c := aws.NewWith(aws.Options{Location: "eu-central-1", Tags: "owner"}, f, f)
	check := model.Check{Machine: "web", ServerName: "claude-web", KeyName: "claude-login", KeyFile: "/k", PublicKey: line,
		Spec: model.ServerSpec{Name: "claude-web", Size: "t3.large", Region: "eu-central-1", Image: "ubuntu-24.04"}}
	if got := texts(c.Validate(context.Background(), check)); !strings.Contains(got, `FAIL AWS_TAGS: "owner" is not KEY=VALUE`) {
		t.Errorf("preflight must report the bad tags: %s", got)
	}
}

func TestValidateFailsOnBadSharedTags(t *testing.T) {
	line, _ := edKey(1)
	f := newFakeAWS()
	c := aws.NewWith(aws.Options{Location: "eu-central-1", SharedTags: "aws:x=1"}, f, f)
	check := model.Check{Machine: "web", ServerName: "claude-web", KeyName: "claude-login", KeyFile: "/k", PublicKey: line,
		Spec: model.ServerSpec{Name: "claude-web", Size: "t3.large", Region: "eu-central-1", Image: "ubuntu-24.04"}}
	if got := texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL AWS_TAGS_SHARED: the key aws:x starts with aws:") {
		t.Errorf("preflight must report the bad shared tags: %s", got)
	}
}

func TestValidate(t *testing.T) {
	line, fp := edKey(1)
	f := newFakeAWS()
	c := client(f, "eu-central-1")
	check := model.Check{Machine: "web", ServerName: "claude-web", KeyName: "claude-login", KeyFile: "/k", PublicKey: line,
		Spec: model.ServerSpec{Name: "claude-web", Size: "t3.large", Region: "eu-central-1", Image: "ubuntu-24.04"}}
	got := texts(c.Validate(context.Background(), check))
	for _, want := range []string{
		"ok AWS credentials can read EC2 in eu-central-1",
		"ok instance type t3.large exists (2 vCPU, 8 GB RAM), architecture x86_64",
		"ok image ubuntu-24.04 is ami-0123456789abcdef0",
		"ok subnet subnet-a in eu-central-1a (VPC vpc-1)",
		"info login key will be uploaded as claude-login",
		"info server claude-web will be created",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	f.keys = []types.KeyPairInfo{{KeyName: ptr("claude-login"), KeyFingerprint: ptr("other")}}
	f.instances["i-1"] = &types.Instance{InstanceId: ptr("i-1"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, Tags: []types.Tag{{Key: ptr("viper"), Value: ptr("claude-web")}}}
	got = texts(c.Validate(context.Background(), check))
	for _, want := range []string{"FAIL key pair claude-login in AWS belongs to a different public key than /k.pub", "info server claude-web exists; viper up reuses it"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	f.keys = []types.KeyPairInfo{{KeyName: ptr("mine"), KeyFingerprint: ptr(fp)}}
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, `ok login key already in eu-central-1 as "mine" (reused)`) {
		t.Errorf("reuse not shown:\n%s", got)
	}

	check.Spec.Size, check.Spec.Image = "nope.huge", "ubuntu-99.99"
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL instance type nope.huge does not exist in eu-central-1") {
		t.Errorf("unknown type not shown:\n%s", got)
	}
	check.Spec.Size = "t3.large"
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL image ubuntu-99.99 does not exist for x86_64 in eu-central-1") {
		t.Errorf("unknown image not shown:\n%s", got)
	}

	f.subnets = nil
	c = client(f, "eu-central-1") // the first client has found its subnet already
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL no default subnet in eu-central-1: set AWS_SUBNET_ID") {
		t.Errorf("missing subnet not shown:\n%s", got)
	}

	f.listErr = apiError("AuthFailure", "AWS was not able to validate the provided access credentials")
	got = texts(c.Validate(context.Background(), check))
	if !strings.Contains(got, "FAIL AWS API not usable (credentials, region, network): AuthFailure: AWS was not able") || strings.Count(got, "\n") != 0 {
		t.Errorf("an unusable API stops at once:\n%s", got)
	}
}

func texts(fs []model.Finding) string {
	names := map[model.Level]string{model.OK: "ok", model.Info: "info", model.Warn: "WARN", model.Fail: "FAIL"}
	var b strings.Builder
	for i, f := range fs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(names[f.Level] + " " + f.Text)
	}
	return b.String()
}

func TestDescribeKeepsTheCodeAndHidesTheRest(t *testing.T) {
	c := client(newFakeAWS(), "eu-central-1")
	if got := c.Describe(apiError("UnauthorizedOperation", "You are not authorized")); got != "UnauthorizedOperation: You are not authorized" {
		t.Error(got)
	}
	if got := c.Describe(errors.New("failed to retrieve credentials: no EC2 IMDS role found")); !strings.Contains(got, "aws login") {
		t.Error(got)
	}
	if c.Name() != "AWS" || c.Title() != "AWS EC2" || !strings.Contains(c.Hint(), "AWS_PROFILE") {
		t.Error("names")
	}
}
