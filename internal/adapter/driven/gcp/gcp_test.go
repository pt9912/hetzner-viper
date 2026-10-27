package gcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"google.golang.org/api/googleapi"
)

// fake is a project with state: what the adapter creates is there for the next call.
type fake struct {
	instances map[string]*gcp.Instance
	rules     map[string]*gcp.Rule
	machines  map[string]gcp.MachineType
	families  map[string]string // "project/family" -> link
	networks  map[string]bool

	launches []gcp.Launch
	deleted  []string
	added    []string // "<rule> ssh icmp"
	created  []gcp.Rule
	looks    int
	listErr  error
}

func newFake() *fake {
	return &fake{
		instances: map[string]*gcp.Instance{},
		rules:     map[string]*gcp.Rule{},
		machines: map[string]gcp.MachineType{
			"e2-standard-2":  {VCPUs: 2, MemoryMB: 8192},
			"t2a-standard-1": {VCPUs: 1, MemoryMB: 4096},
		},
		families: map[string]string{
			"ubuntu-os-cloud/ubuntu-2404-lts-amd64": "projects/ubuntu-os-cloud/global/images/ubuntu-2404-amd64-v1",
			"ubuntu-os-cloud/ubuntu-2404-lts-arm64": "projects/ubuntu-os-cloud/global/images/ubuntu-2404-arm64-v1",
			"ubuntu-os-cloud/ubuntu-2204-lts":       "projects/ubuntu-os-cloud/global/images/ubuntu-2204-v1",
		},
		networks: map[string]bool{"default": true},
	}
}

func (f *fake) Instance(_ context.Context, name string) (*gcp.Instance, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	i, ok := f.instances[name]
	if !ok {
		return nil, nil
	}
	// a new VM is staging at its first look, then it runs with an address
	f.looks++
	if i.Status == "STAGING" && f.looks >= 2 {
		i.Status, i.IP = "RUNNING", "203.0.113.20"
	}
	cp := *i
	return &cp, nil
}

func (f *fake) CreateInstance(_ context.Context, l gcp.Launch) error {
	f.launches = append(f.launches, l)
	f.instances[l.Name] = &gcp.Instance{Name: l.Name, Status: "STAGING", Created: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	return nil
}

func (f *fake) DeleteInstance(_ context.Context, name string) error {
	if _, ok := f.instances[name]; !ok {
		return &googleapi.Error{Code: 404, Message: "not found"}
	}
	f.deleted = append(f.deleted, name)
	delete(f.instances, name)
	return nil
}

func (f *fake) Firewall(_ context.Context, name string) (*gcp.Rule, error) {
	if r, ok := f.rules[name]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, nil
}

func (f *fake) CreateFirewall(_ context.Context, r gcp.Rule) error {
	f.created = append(f.created, r)
	f.rules[r.Name] = &r
	return nil
}

func (f *fake) AddFirewallAllowed(_ context.Context, name string, ssh, icmp bool) error {
	f.added = append(f.added, name+" ssh="+boolStr(ssh)+" icmp="+boolStr(icmp))
	r := f.rules[name]
	r.SSH, r.ICMP = r.SSH || ssh, r.ICMP || icmp
	return nil
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (f *fake) MachineType(_ context.Context, name string) (*gcp.MachineType, error) {
	m, ok := f.machines[name]
	if !ok {
		return nil, nil
	}
	return &m, nil
}

func (f *fake) ImageFamily(_ context.Context, project, family string) (string, error) {
	return f.families[project+"/"+family], nil
}

func (f *fake) NetworkExists(_ context.Context, name string) (bool, error) {
	return f.networks[name], nil
}

func client(f *fake) *gcp.Client {
	return gcp.NewWith(gcp.Options{Project: "my-project", Zone: "europe-west3-b", Poll: 1}, f)
}

func TestCreateServerStartsTheVMAndWaitsForItsAddress(t *testing.T) {
	f := newFake()
	srv, err := client(f).CreateServer(context.Background(), model.ServerSpec{
		Name: "claude-web", Size: "e2-standard-2", Image: "ubuntu-24.04", SSHKey: "ssh-ed25519 AAAA k", Firewall: "claude-ssh", UserData: "#cloud-config\nx: 1\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != "claude-web" || srv.Name != "claude-web" || srv.IPv4 != "203.0.113.20" || srv.Created.IsZero() {
		t.Errorf("%+v", srv)
	}
	l := f.launches[0]
	if l.MachineType != "e2-standard-2" || !strings.HasSuffix(l.Image, "ubuntu-2404-amd64-v1") || l.Network != "default" || l.Tag != "claude-ssh" || l.DiskGB != 50 || l.UserData != "#cloud-config\nx: 1\n" {
		t.Errorf("launch %+v", l)
	}
	if got, err := client(f).ServerByName(context.Background(), "claude-web"); err != nil || got == nil || got.ID != "claude-web" {
		t.Errorf("ServerByName: %+v %v", got, err)
	}
	if got, err := client(f).ServerByName(context.Background(), "other"); err != nil || got != nil {
		t.Errorf("another name is no server: %+v %v", got, err)
	}
}

func TestAnArmMachineTypeGetsTheArmImage(t *testing.T) {
	f := newFake()
	if _, err := client(f).CreateServer(context.Background(), model.ServerSpec{Name: "x", Size: "t2a-standard-1", Image: "ubuntu-24.04", Firewall: "claude-ssh"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.launches[0].Image, "ubuntu-2404-arm64-v1") {
		t.Errorf("image %s", f.launches[0].Image)
	}
	// an LTS without an amd64 family falls back to the family without a suffix
	f = newFake()
	if _, err := client(f).CreateServer(context.Background(), model.ServerSpec{Name: "x", Size: "e2-standard-2", Image: "ubuntu-22.04", Firewall: "claude-ssh"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.launches[0].Image, "ubuntu-2204-v1") {
		t.Errorf("image %s", f.launches[0].Image)
	}
}

func TestCreateServerFailsBeforeCreatingAnything(t *testing.T) {
	spec := model.ServerSpec{Name: "x", Size: "e2-standard-2", Image: "ubuntu-24.04", Firewall: "claude-ssh"}
	for name, mod := range map[string]func(*fake, *model.ServerSpec){
		"unknown type":  func(_ *fake, s *model.ServerSpec) { s.Size = "nope-1" },
		"unknown image": func(_ *fake, s *model.ServerSpec) { s.Image = "ubuntu-99.99" },
		"bad image":     func(_ *fake, s *model.ServerSpec) { s.Image = "debian" },
		"no network":    func(f *fake, _ *model.ServerSpec) { f.networks = map[string]bool{} },
	} {
		f := newFake()
		s := spec
		mod(f, &s)
		if _, err := client(f).CreateServer(context.Background(), s); err == nil || len(f.launches) != 0 {
			t.Errorf("%s: err %v, launches %d", name, err, len(f.launches))
		}
	}
}

func TestDeleteServer(t *testing.T) {
	f := newFake()
	c := client(f)
	srv, err := c.CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "e2-standard-2", Image: "ubuntu-24.04", Firewall: "claude-ssh"})
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

func TestEnsureFirewallMakesOneRulePerAddressFamily(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	c := client(f)
	res, err := c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8", "::/0", "192.0.2.0/24"})
	if err != nil || !res.Created || res.Complete {
		t.Fatalf("first: %+v %v", res, err)
	}
	if len(f.created) != 2 || f.created[0].Name != "claude-ssh" || len(f.created[0].Ranges) != 2 || f.created[1].Name != "claude-ssh-ipv6" || f.created[1].Ranges[0] != "::/0" {
		t.Fatalf("created %+v", f.created)
	}
	for _, r := range f.created {
		if r.Tag != "claude-ssh" || r.Network != "default" || !r.SSH || !r.ICMP {
			t.Errorf("rule %+v: it applies to the machines tagged with the firewall name, for SSH and ping", r)
		}
	}
	res, err = c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8", "::/0"})
	if err != nil || res.Created || !res.Complete || len(f.created) != 2 || len(f.added) != 0 {
		t.Errorf("second: %+v %v, created %d, added %v", res, err, len(f.created), f.added)
	}
	// a rule that lets only SSH in gets the ping permission
	f.rules["claude-ssh"].ICMP = false
	res, err = c.EnsureFirewall(ctx, "claude-ssh", []string{"10.0.0.0/8"})
	if err != nil || res.Complete || len(f.added) != 1 || f.added[0] != "claude-ssh ssh=no icmp=yes" {
		t.Errorf("third: %+v %v, added %v", res, err, f.added)
	}
	// IPv4 networks only: no IPv6 rule
	f = newFake()
	if _, err := client(f).EnsureFirewall(ctx, "x-ssh", []string{"10.0.0.0/8"}); err != nil || len(f.created) != 1 {
		t.Errorf("created %+v %v", f.created, err)
	}
}

func TestEnsureFirewallRefusesWhatGCPWouldRejectOrIgnore(t *testing.T) {
	f := newFake()
	c := client(f)
	if _, err := c.EnsureFirewall(context.Background(), "claude-ssh", []string{" ", ""}); err == nil || len(f.created) != 0 {
		t.Errorf("no network at all must be an error, not a rule-less firewall: %v", err)
	}
	long := strings.Repeat("a", 60) + "-ssh"
	if _, err := c.EnsureFirewall(context.Background(), long, []string{"::/0"}); err == nil || !strings.Contains(err.Error(), "63") || len(f.created) != 0 {
		t.Errorf("a rule name over 63 characters: %v", err)
	}
}

func TestValidate(t *testing.T) {
	f := newFake()
	c := client(f)
	check := model.Check{ServerName: "claude-web", KeyName: "claude-login", KeyFile: "/k", PublicKey: "ssh-ed25519 AAAA",
		Spec: model.ServerSpec{Name: "claude-web", Size: "e2-standard-2", Image: "ubuntu-24.04"}}
	got := texts(c.Validate(context.Background(), check))
	for _, want := range []string{
		"ok Google credentials can read project my-project in europe-west3-b",
		"ok machine type e2-standard-2 exists (2 vCPU, 8 GB RAM), architecture x86_64",
		"ok image ubuntu-24.04 is projects/ubuntu-os-cloud/global/images/ubuntu-2404-amd64-v1",
		"ok network default exists",
		"info server claude-web will be created",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	check.Spec.Size, check.Spec.Image = "nope-1", "ubuntu-99.99"
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL machine type nope-1 does not exist in europe-west3-b") {
		t.Errorf("unknown type not shown:\n%s", got)
	}
	check.Spec.Size = "e2-standard-2"
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL image ubuntu-99.99 does not exist for x86_64") {
		t.Errorf("unknown image not shown:\n%s", got)
	}
	f.networks = map[string]bool{}
	if got = texts(c.Validate(context.Background(), check)); !strings.Contains(got, "FAIL network default does not exist in project my-project: set GCP_NETWORK") {
		t.Errorf("missing network not shown:\n%s", got)
	}
	f.listErr = &googleapi.Error{Code: 403, Message: "Compute Engine API has not been used in project"}
	got = texts(c.Validate(context.Background(), check))
	if !strings.Contains(got, "FAIL Google Cloud API not usable (credentials, project, network): 403: Compute Engine API has not been used") || strings.Count(got, "\n") != 0 {
		t.Errorf("an unusable API stops at once:\n%s", got)
	}
	noProject := gcp.NewWith(gcp.Options{}, f)
	if got = texts(noProject.Validate(context.Background(), check)); !strings.Contains(got, "FAIL GCP_PROJECT is not set") {
		t.Errorf("no project:\n%s", got)
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

func TestOperationsThatFailAreReportedAsOpErrors(t *testing.T) {
	var _ cloud.Provider = client(newFake())
	c := gcp.New(gcp.Options{})
	if _, err := c.ServerByName(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "GCP_PROJECT") {
		t.Errorf("without a project: %v", err)
	}
	if got := c.Describe(&googleapi.Error{Code: 403, Message: "denied"}); got != "403: denied" {
		t.Error(got)
	}
	if got := c.Describe(errors.New("google: could not find default credentials. See https://cloud.google.com")); !strings.Contains(got, "gcloud auth application-default login") {
		t.Error(got)
	}
	if c.Name() != "Google Cloud" || c.Title() != "Google Compute Engine" || !strings.Contains(c.Hint(), "GCP_PROJECT") {
		t.Error("names")
	}
}
