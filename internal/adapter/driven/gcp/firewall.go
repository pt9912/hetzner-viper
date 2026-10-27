package gcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// EnsureLoginKey gives the key itself as its reference: GCE keeps no key objects, the key goes
// into the VM's cloud-init.
func (*Client) EnsureLoginKey(_ context.Context, _, publicKey string) (model.LoginKey, error) {
	return model.LoginKey{Ref: strings.TrimSpace(publicKey)}, nil
}

// split sorts the networks into IPv4 and IPv6 ones: a firewall rule takes only one kind.
func split(sources []string) (v4, v6 []string) {
	for _, s := range sources {
		s = strings.TrimSpace(s)
		if strings.Contains(s, ":") {
			v6 = append(v6, s)
		} else if s != "" {
			v4 = append(v4, s)
		}
	}
	return v4, v6
}

// EnsureFirewall makes sure the firewall rules exist and let SSH (TCP 22) and ping in from the
// networks to the VMs tagged with the name: <name> for IPv4 and <name>-ipv6 for IPv6 networks. A
// missing permission is added (also to an existing rule); other rules are left alone.
func (c *Client) EnsureFirewall(ctx context.Context, name string, sources []string) (model.FirewallResult, error) {
	var res model.FirewallResult
	api, err := c.client(ctx)
	if err != nil {
		return res, &cloud.OpError{Op: "reaching Google Cloud", Err: err}
	}
	v4, v6 := split(sources)
	res.Complete = true
	for _, r := range []Rule{
		{Name: name, Ranges: v4},
		{Name: name + "-ipv6", Ranges: v6},
	} {
		if len(r.Ranges) == 0 {
			continue
		}
		r.Tag, r.Network, r.SSH, r.ICMP = name, c.opts.network(), true, true
		created, complete, err := ensureRule(ctx, api, r)
		if err != nil {
			return res, err
		}
		res.Created = res.Created || created
		res.Complete = res.Complete && complete && !created
	}
	return res, nil
}

// ensureRule creates the rule or completes an existing one.
func ensureRule(ctx context.Context, api API, want Rule) (created, complete bool, err error) {
	have, err := api.Firewall(ctx, want.Name)
	if err != nil {
		return false, false, &cloud.OpError{Op: "listing the firewall rules", Err: err}
	}
	if have == nil {
		if err := api.CreateFirewall(ctx, want); err != nil {
			return false, false, &cloud.OpError{Op: fmt.Sprintf("creating the firewall rule %s", want.Name), Write: true, Err: err}
		}
		return true, false, nil
	}
	if have.SSH && have.ICMP {
		return false, true, nil
	}
	if err := api.AddFirewallAllowed(ctx, want.Name, !have.SSH, !have.ICMP); err != nil {
		return false, false, &cloud.OpError{Op: fmt.Sprintf("adding SSH and ping to the firewall rule %s", want.Name), Write: true, Err: err}
	}
	return false, false, nil
}
