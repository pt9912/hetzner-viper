package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// EnsureLoginKey gives the key itself as its reference: Azure keeps no key objects, the key goes
// with the VM.
func (*Client) EnsureLoginKey(_ context.Context, _, publicKey string) (model.LoginKey, error) {
	return model.LoginKey{Ref: strings.TrimSpace(publicKey)}, nil
}

// EnsureFirewall makes the network (resource group, virtual network, subnet) and the network
// security group, and makes sure the group lets SSH (TCP 22) and ping in from the networks. A
// missing rule is added (also to an existing group); other rules are left alone.
func (c *Client) EnsureFirewall(ctx context.Context, name string, sources []string) (model.FirewallResult, error) {
	var res model.FirewallResult
	api, err := c.client(ctx)
	if err != nil {
		return res, &cloud.OpError{Op: "reaching Azure", Err: err}
	}
	// the server has an IPv4 address only: IPv6 networks could never reach it, and an empty list
	// would make a rule that lets nobody in
	sources = ipv4Only(sources)
	if len(sources) == 0 {
		return res, &cloud.OpError{Op: "reading the networks", Err: fmt.Errorf("SSH_ALLOWED_CIDRS names no IPv4 network (an Azure server here has an IPv4 address only)")}
	}
	if _, err := api.EnsureNetwork(ctx); err != nil {
		return res, &cloud.OpError{Op: fmt.Sprintf("making the network in resource group %s", c.opts.group()), Write: true, Err: err}
	}
	have, err := api.NSG(ctx, name)
	if err != nil {
		return res, &cloud.OpError{Op: "listing the network security groups", Err: err}
	}
	if have == nil {
		if err := api.CreateNSG(ctx, name, sources); err != nil {
			return res, &cloud.OpError{Op: fmt.Sprintf("creating the network security group %s", name), Write: true, Err: err}
		}
		res.Created = true
		return res, nil
	}
	res.Complete = have.SSH && have.ICMP
	if !res.Complete {
		if err := api.AddNSGRules(ctx, name, !have.SSH, !have.ICMP, sources); err != nil {
			return res, &cloud.OpError{Op: fmt.Sprintf("adding SSH and ping to network security group %s", name), Write: true, Err: err}
		}
	}
	return res, nil
}

// ipv4Only drops the blank entries and the IPv6 networks.
func ipv4Only(sources []string) []string {
	var out []string
	for _, s := range sources {
		if s = strings.TrimSpace(s); s != "" && !strings.Contains(s, ":") {
			out = append(out, s)
		}
	}
	return out
}
