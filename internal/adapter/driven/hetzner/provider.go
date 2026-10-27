package hetzner

import (
	"context"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// Identity is what names the provider; Client and Validator embed it, and so does the fake of
// the tests (internal/servertest), which stands in for a Hetzner project.
type Identity struct{}

// Name is the short name of the provider in messages.
func (Identity) Name() string { return "Hetzner" }

// Title is the name of the provider in the preflight.
func (Identity) Title() string { return "Hetzner Cloud" }

// Describe is an API error without the request details, safe to show.
func (Identity) Describe(err error) string { return Error(err) }

// Hint says what to check when the API cannot be used.
func (Identity) Hint() string { return "HCLOUD_TOKEN / project / network" }

// Validate reads the project and says whether the server can be created.
func (h *Client) Validate(ctx context.Context, c model.Check) []model.Finding {
	return Validator{API: h}.Validate(ctx, c)
}

// CreateServer creates the server and waits until it is running.
func (h *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	return h.ServerCreate(ctx, ServerSpec{Name: s.Name, Type: s.Size, Location: s.Region, Image: s.Image, SSHKey: s.SSHKey, Firewall: s.Firewall, UserData: s.UserData})
}

// EnsureLoginKey makes sure the public key is in the project; see the function of that name.
func (h *Client) EnsureLoginKey(ctx context.Context, name, publicKey string) (model.LoginKey, error) {
	return EnsureLoginKey(ctx, h, name, publicKey)
}

// EnsureFirewall makes sure the firewall allows SSH and ping; see the function of that name.
func (h *Client) EnsureFirewall(ctx context.Context, name string, sources []string) (model.FirewallResult, error) {
	return EnsureFirewall(ctx, h, name, sources)
}

// Keys is the part of the project's SSH keys the login key is made sure with.
type Keys interface {
	SSHKeys(ctx context.Context) ([]SSHKey, error)
	SSHKeyCreate(ctx context.Context, name, publicKey string) error
}

// EnsureLoginKey makes sure the public key is in the project and returns the name it has there.
// Hetzner allows each public key only once per project: it is reused under whatever name it has.
func EnsureLoginKey(ctx context.Context, api Keys, name, publicKey string) (model.LoginKey, error) {
	fp := model.FingerprintMD5(publicKey)
	keys, err := api.SSHKeys(ctx)
	if err != nil {
		return model.LoginKey{}, &cloud.OpError{Op: "listing the SSH keys", Err: err}
	}
	for _, k := range keys {
		if fp != "" && strings.EqualFold(k.Fingerprint, fp) {
			return model.LoginKey{Ref: k.Name}, nil
		}
	}
	for _, k := range keys {
		if k.Name == name {
			return model.LoginKey{}, &cloud.KeyConflictError{Name: name}
		}
	}
	if err := api.SSHKeyCreate(ctx, name, publicKey); err != nil {
		return model.LoginKey{}, &cloud.OpError{Op: fmt.Sprintf("uploading the SSH key %s", name), Write: true, Err: err}
	}
	return model.LoginKey{Ref: name, Created: true}, nil
}

// Firewalls is the part of the project's firewalls the SSH rules are made sure with.
type Firewalls interface {
	FirewallRules(ctx context.Context, name string) ([]FirewallRule, bool, error)
	FirewallCreate(ctx context.Context, name string) error
	FirewallAddRule(ctx context.Context, name string, r FirewallRule) error
}

// EnsureFirewall makes sure the firewall exists and has the rules for SSH (TCP 22) and ICMP
// (ping, for diagnosis) from the networks. A missing rule is added (also to an existing firewall);
// other rules are left alone.
func EnsureFirewall(ctx context.Context, api Firewalls, name string, sources []string) (model.FirewallResult, error) {
	var res model.FirewallResult
	rules, exists, err := api.FirewallRules(ctx, name)
	if err != nil {
		return res, &cloud.OpError{Op: "listing the firewalls", Err: err}
	}
	if !exists {
		if err := api.FirewallCreate(ctx, name); err != nil {
			return res, &cloud.OpError{Op: fmt.Sprintf("creating the firewall %s", name), Write: true, Err: err}
		}
		res.Created = true
		rules = nil
	}
	has := func(proto, port string) bool {
		for _, rule := range rules {
			if rule.Direction == "in" && rule.Protocol == proto && (port == "" || rule.Port == port) {
				return true
			}
		}
		return false
	}
	res.Complete = has("tcp", "22") && has("icmp", "")
	if !has("tcp", "22") {
		if err := api.FirewallAddRule(ctx, name, FirewallRule{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: sources}); err != nil {
			return res, &cloud.OpError{Op: fmt.Sprintf("adding the SSH rule to firewall %s", name), Write: true, Err: err}
		}
	}
	if !has("icmp", "") {
		if err := api.FirewallAddRule(ctx, name, FirewallRule{Direction: "in", Protocol: "icmp", SourceIPs: sources}); err != nil {
			return res, &cloud.OpError{Op: fmt.Sprintf("adding the ICMP rule to firewall %s", name), Write: true, Err: err}
		}
	}
	return res, nil
}

// Reader is the part of the Hetzner API the checks read.
type Reader interface {
	ServerNames(ctx context.Context) ([]string, error)
	ServerType(ctx context.Context, name string) (*ServerType, error)
	LocationExists(ctx context.Context, name string) (bool, error)
	ImageExists(ctx context.Context, name, architecture string) (bool, error)
	SSHKeys(ctx context.Context) ([]SSHKey, error)
}

// Validator is the preflight of the Hetzner project over a Reader.
type Validator struct {
	Identity
	API Reader
}

// Validate says whether the server can be created: the token works, the server type is offered
// in the location, the image exists, the login key fits.
func (v Validator) Validate(ctx context.Context, c model.Check) []model.Finding {
	var f findings
	names, err := v.API.ServerNames(ctx)
	if err != nil {
		f.bad("Hetzner API not usable (token, project, network): %s", Error(err))
		return f
	}
	f.ok("Hetzner token can read the project (write access is only seen when viper up creates something)")
	v.serverTypeChecks(ctx, &f, c)
	keys, err := v.API.SSHKeys(ctx)
	if err != nil {
		f.bad("SSH keys could not be listed: %s", Error(err))
		return f
	}
	sshKeyChecks(&f, c, keys)
	for _, n := range names {
		if n == c.ServerName {
			f.info("server %s exists; viper up reuses it", c.ServerName)
			return f
		}
	}
	f.info("server %s will be created", c.ServerName)
	return f
}

type findings []model.Finding

func (f *findings) add(l model.Level, format string, a ...any) {
	*f = append(*f, model.Finding{Level: l, Text: fmt.Sprintf(format, a...)})
}
func (f *findings) ok(format string, a ...any)   { f.add(model.OK, format, a...) }
func (f *findings) info(format string, a ...any) { f.add(model.Info, format, a...) }
func (f *findings) warn(format string, a ...any) { f.add(model.Warn, format, a...) }
func (f *findings) bad(format string, a ...any)  { f.add(model.Fail, format, a...) }

func formatGB(m float32) string { return fmt.Sprintf("%g", m) }

// serverTypeChecks checks the server type, its location and the image.
func (v Validator) serverTypeChecks(ctx context.Context, f *findings, c model.Check) {
	size, image := c.Spec.Size, c.Spec.Image
	st, err := v.API.ServerType(ctx, size)
	switch {
	case err != nil:
		f.bad("server type %s could not be read: %s", size, Error(err))
	case st == nil:
		f.bad("server type %s does not exist (see the server types in the Hetzner Console)", size)
	default:
		f.ok("server type %s exists (%d vCPU, %s GB RAM, %d GB disk), architecture %s", size, st.Cores, formatGB(st.MemoryGB), st.DiskGB, st.Architecture)
		v.locationChecks(ctx, f, c, st)
		if ok, err := v.API.ImageExists(ctx, image, st.Architecture); err != nil {
			f.bad("image %s could not be read: %s", image, Error(err))
		} else if ok {
			f.ok("image %s exists for %s", image, st.Architecture)
		} else {
			f.bad("image %s does not exist for %s (see the system images in the Hetzner Console)", image, st.Architecture)
		}
	}
}

// sshKeyChecks compares the login key with the keys in the project.
func sshKeyChecks(f *findings, c model.Check, keys []SSHKey) {
	fp := model.FingerprintMD5(c.PublicKey)
	existing, named := "", false
	for _, k := range keys {
		if fp != "" && strings.EqualFold(k.Fingerprint, fp) && existing == "" {
			existing = k.Name
		}
		if k.Name == c.KeyName {
			named = true
		}
	}
	switch {
	case existing != "":
		f.ok("login key already in the project as \"%s\" (reused)", existing)
	case named:
		f.bad("SSH key %s in Hetzner belongs to a different public key than %s.pub", c.KeyName, c.KeyFile)
	default:
		f.info("login key will be uploaded as %s", c.KeyName)
	}
}

func (v Validator) locationChecks(ctx context.Context, f *findings, c model.Check, st *ServerType) {
	size, region := c.Spec.Size, c.Spec.Region
	exists, err := v.API.LocationExists(ctx, region)
	if err != nil {
		f.bad("location %s could not be read: %s", region, Error(err))
		return
	}
	if !exists {
		f.bad("location %s does not exist (see the locations in the Hetzner Console)", region)
		return
	}
	f.ok("location %s exists", region)
	// availability per location comes with the server type (the datacenter API was removed)
	var offered []string
	var here *ServerTypeLocation
	for i, l := range st.Locations {
		if l.Available {
			offered = append(offered, l.Name)
		}
		if l.Name == region {
			here = &st.Locations[i]
		}
	}
	list := strings.Join(offered, ", ")
	if list == "" {
		list = "none"
	}
	hint := fmt.Sprintf("set SERVER_LOCATION or SERVER_TYPE, e.g. in machines/%s.env.local", c.Machine)
	switch {
	case here == nil:
		f.bad("%s is not offered in %s (available in: %s); %s", size, region, list, hint)
	case !here.Available:
		f.bad("%s cannot currently be ordered in %s (available in: %s); %s", size, region, list, hint)
	default:
		f.ok("%s can currently be ordered in %s", size, region)
		if !here.UnavailableAfter.IsZero() {
			f.warn("%s is deprecated in %s and unavailable after %s", size, region, here.UnavailableAfter.UTC().Format("2006-01-02T15:04:05Z"))
		}
	}
}
