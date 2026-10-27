package azure_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// fake is a resource group with state: what the adapter creates is there for the next call.
type fake struct {
	vms      map[string]*azure.VM
	nsgs     map[string]*azure.NSG
	sizes    map[string]azure.Size
	images   map[string]bool
	subnet   string
	networks int

	launches []azure.Launch
	deleted  []string
	rules    []string // "<group> ssh icmp"
	created  []string
	looks    int
	listErr  error
}

func newFake() *fake {
	return &fake{
		vms:  map[string]*azure.VM{},
		nsgs: map[string]*azure.NSG{},
		sizes: map[string]azure.Size{
			"Standard_D2s_v5":  {VCPUs: 2, MemoryMB: 8192},
			"Standard_D2ps_v5": {VCPUs: 2, MemoryMB: 8192},
		},
		images: map[string]bool{
			"Canonical:ubuntu-24_04-lts:server:latest":                     true,
			"Canonical:ubuntu-24_04-lts:server-arm64:latest":               true,
			"Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest": true,
		},
	}
}

func (f *fake) VM(_ context.Context, name string) (*azure.VM, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	v, ok := f.vms[name]
	if !ok {
		return nil, nil
	}
	// the public address shows at the second look
	f.looks++
	if f.looks >= 2 {
		v.IP = "203.0.113.30"
	}
	cp := *v
	return &cp, nil
}

func (f *fake) CreateVM(_ context.Context, l azure.Launch) error {
	f.launches = append(f.launches, l)
	f.vms[l.Name] = &azure.VM{Name: l.Name, Created: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	return nil
}

func (f *fake) DeleteVM(_ context.Context, name string) error {
	if _, ok := f.vms[name]; !ok {
		return errors.New("ResourceNotFound")
	}
	f.deleted = append(f.deleted, name)
	delete(f.vms, name)
	return nil
}

func (f *fake) Subnet(context.Context) (string, error) { return f.subnet, nil }

func (f *fake) EnsureNetwork(context.Context) (string, error) {
	if f.subnet == "" {
		f.subnet = "/subscriptions/s/resourceGroups/viper/providers/Microsoft.Network/virtualNetworks/viper-vnet/subnets/viper-subnet"
		f.networks++
	}
	return f.subnet, nil
}

func (f *fake) NSG(_ context.Context, name string) (*azure.NSG, error) {
	if n, ok := f.nsgs[name]; ok {
		cp := *n
		return &cp, nil
	}
	return nil, nil
}

func (f *fake) CreateNSG(_ context.Context, name string, sources []string) error {
	f.created = append(f.created, name+" "+strings.Join(sources, ","))
	f.nsgs[name] = &azure.NSG{SSH: true, ICMP: true}
	return nil
}

func (f *fake) AddNSGRules(_ context.Context, name string, ssh, icmp bool, _ []string) error {
	f.rules = append(f.rules, name+" ssh="+yes(ssh)+" icmp="+yes(icmp))
	n := f.nsgs[name]
	n.SSH, n.ICMP = n.SSH || ssh, n.ICMP || icmp
	return nil
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (f *fake) VMSize(_ context.Context, name string) (*azure.Size, error) {
	s, ok := f.sizes[name]
	if !ok {
		return nil, nil
	}
	return &s, nil
}

func (f *fake) ImageExists(_ context.Context, img azure.Image) (bool, error) {
	return f.images[img.String()], nil
}

func client(f *fake) *azure.Client {
	return azure.NewWith(azure.Options{SubscriptionID: "sub-1", Location: "germanywestcentral", Poll: 1}, f)
}

func prepare(t *testing.T, f *fake, c *azure.Client) {
	t.Helper()
	if _, err := c.EnsureFirewall(context.Background(), "claude-ssh", []string{"10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateServerMakesTheVMAndWaitsForItsAddress(t *testing.T) {
	f := newFake()
	c := client(f)
	prepare(t, f, c)
	srv, err := c.CreateServer(context.Background(), model.ServerSpec{
		Name: "claude-web", Size: "Standard_D2s_v5", Image: "ubuntu-24.04", SSHKey: "ssh-ed25519 AAAA k", Firewall: "claude-ssh", UserData: "#cloud-config\nx: 1\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != "claude-web" || srv.IPv4 != "203.0.113.30" || srv.Created.IsZero() {
		t.Errorf("%+v", srv)
	}
	l := f.launches[0]
	if l.Image.String() != "Canonical:ubuntu-24_04-lts:server:latest" || l.NSG != "claude-ssh" || l.DiskGB != 64 || l.SSHKey != "ssh-ed25519 AAAA k" || l.UserData != "#cloud-config\nx: 1\n" || !strings.Contains(l.SubnetID, "viper-subnet") {
		t.Errorf("launch %+v", l)
	}
	if got, err := c.ServerByName(context.Background(), "claude-web"); err != nil || got == nil || got.ID != "claude-web" {
		t.Errorf("ServerByName: %+v %v", got, err)
	}
	if got, err := c.ServerByName(context.Background(), "other"); err != nil || got != nil {
		t.Errorf("another name is no server: %+v %v", got, err)
	}
}

func TestImagesByArchitectureAndByUrn(t *testing.T) {
	f := newFake()
	c := client(f)
	prepare(t, f, c)
	for size, want := range map[string]string{
		"Standard_D2ps_v5": "Canonical:ubuntu-24_04-lts:server-arm64:latest",
		"Standard_D2s_v5":  "Canonical:ubuntu-24_04-lts:server:latest",
	} {
		f.launches = nil
		if _, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "x" + size[len(size)-5:], Size: size, Image: "ubuntu-24.04", Firewall: "claude-ssh"}); err != nil {
			t.Fatal(err)
		}
		if got := f.launches[0].Image.String(); got != want {
			t.Errorf("%s: image %s, want %s", size, got, want)
		}
	}
	f.launches = nil
	if _, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "y", Size: "Standard_D2s_v5", Image: "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest", Firewall: "claude-ssh"}); err != nil || len(f.launches) != 1 {
		t.Errorf("a URN: %v", err)
	}
}

func TestCreateServerFailsBeforeCreatingAnything(t *testing.T) {
	spec := model.ServerSpec{Name: "x", Size: "Standard_D2s_v5", Image: "ubuntu-24.04", Firewall: "claude-ssh"}
	for name, mod := range map[string]func(*fake, *model.ServerSpec){
		"unknown size":      func(_ *fake, s *model.ServerSpec) { s.Size = "Standard_Nope" },
		"unknown image":     func(_ *fake, s *model.ServerSpec) { s.Image = "ubuntu-99.99" },
		"missing image":     func(f *fake, _ *model.ServerSpec) { f.images = map[string]bool{} },
		"no arm on 22.04":   func(_ *fake, s *model.ServerSpec) { s.Size, s.Image = "Standard_D2ps_v5", "ubuntu-22.04" },
		"no security group": func(_ *fake, s *model.ServerSpec) { s.Firewall = "missing" },
	} {
		f := newFake()
		c := client(f)
		prepare(t, f, c)
		s := spec
		mod(f, &s)
		if _, err := c.CreateServer(context.Background(), s); err == nil || len(f.launches) != 0 {
			t.Errorf("%s: err %v, launches %d", name, err, len(f.launches))
		}
	}
	f := newFake() // no network yet: the firewall step makes it
	if _, err := client(f).CreateServer(context.Background(), spec); err == nil || !strings.Contains(err.Error(), "no subnet") {
		t.Errorf("without a network: %v", err)
	}
}

func TestDeleteServer(t *testing.T) {
	f := newFake()
	c := client(f)
	prepare(t, f, c)
	srv, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "Standard_D2s_v5", Image: "ubuntu-24.04", Firewall: "claude-ssh"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteServer(context.Background(), srv.ID); err != nil || len(f.deleted) != 1 {
		t.Fatalf("%v %v", err, f.deleted)
	}
	if err := c.DeleteServer(context.Background(), "gone"); err == nil {
		t.Error("deleting an unknown VM must fail")
	}
}

func TestTheLoginKeyIsItsOwnReference(t *testing.T) {
	key, err := client(newFake()).EnsureLoginKey(context.Background(), "claude-login", "ssh-ed25519 AAAA me@laptop\n")
	if err != nil || key.Ref != "ssh-ed25519 AAAA me@laptop" || key.Created {
		t.Errorf("%+v %v", key, err)
	}
}

func TestEnsureFirewall(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	c := client(f)
	res, err := c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8", "::/0"})
	if err != nil || !res.Created || res.Complete || f.networks != 1 || len(f.created) != 1 || f.created[0] != "claude-ssh 10.0.0.0/8,::/0" {
		t.Fatalf("first: %+v %v, networks %d, created %v", res, err, f.networks, f.created)
	}
	res, err = c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8"})
	if err != nil || res.Created || !res.Complete || f.networks != 1 || len(f.rules) != 0 {
		t.Errorf("second: %+v %v, rules %v", res, err, f.rules)
	}
	f.nsgs["claude-ssh"].ICMP = false
	res, err = c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8"})
	if err != nil || res.Complete || len(f.rules) != 1 || f.rules[0] != "claude-ssh ssh=no icmp=yes" {
		t.Errorf("third: %+v %v, rules %v", res, err, f.rules)
	}
	var op *cloud.OpError
	if _, err := azure.NewWith(azure.Options{}, nil).EnsureFirewall(ctx, "x", nil); !errors.As(err, &op) {
		t.Errorf("without a subscription: %v", err)
	}
}

func TestValidate(t *testing.T) {
	f := newFake()
	c := client(f)
	check := model.Check{ServerName: "claude-web", KeyName: "claude-login", KeyFile: "/k", PublicKey: "ssh-ed25519 AAAA",
		Spec: model.ServerSpec{Name: "claude-web", Size: "Standard_D2s_v5", Image: "ubuntu-24.04"}}
	got := texts(c.Validate(context.Background(), check))
	for _, want := range []string{
		"ok Azure credentials can read subscription sub-1",
		"ok VM size Standard_D2s_v5 exists (2 vCPU, 8 GB RAM), architecture x86_64",
		"ok image ubuntu-24.04 is Canonical:ubuntu-24_04-lts:server:latest",
		"info resource group viper with a virtual network will be created",
		"info server claude-web will be created",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	check.Spec.Size, check.Spec.Image = "Standard_Nope", "ubuntu-99.99"
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL VM size Standard_Nope does not exist in germanywestcentral") {
		t.Errorf("unknown size not shown:\n%s", got)
	}
	check.Spec.Size = "Standard_D2s_v5"
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL image ubuntu-99.99 is not supported") {
		t.Errorf("unknown image not shown:\n%s", got)
	}
	check.Spec.Image = "ubuntu-24.04"
	f.images = map[string]bool{}
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL image Canonical:ubuntu-24_04-lts:server:latest does not exist in germanywestcentral") {
		t.Errorf("missing image not shown:\n%s", got)
	}
	f.listErr = errors.New("DefaultAzureCredential: failed to acquire a token")
	got = texts(c.Validate(context.Background(), check))
	if !strings.Contains(got, "FAIL Azure API not usable (credentials, subscription, network): no Azure credentials (run az login)") || strings.Count(got, "\n") != 0 {
		t.Errorf("an unusable API stops at once:\n%s", got)
	}
	if got = texts(azure.NewWith(azure.Options{}, f).Validate(context.Background(), check)); !strings.Contains(got, "FAIL AZURE_SUBSCRIPTION_ID is not set") {
		t.Errorf("no subscription:\n%s", got)
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

func TestNames(t *testing.T) {
	var _ cloud.Provider = client(newFake())
	c := azure.New(azure.Options{})
	if _, err := c.ServerByName(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "AZURE_SUBSCRIPTION_ID") {
		t.Errorf("without a subscription: %v", err)
	}
	if c.Name() != "Azure" || c.Title() != "Microsoft Azure" || !strings.Contains(c.Hint(), "az login") {
		t.Error("names")
	}
	if got := c.Describe(errors.New("DefaultAzureCredential: failed to acquire a token")); !strings.Contains(got, "az login") {
		t.Error(got)
	}
}
