package hetzner

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// SSHKeyCreate uploads a public key to the project under that name.
func (h *Client) SSHKeyCreate(ctx context.Context, name, publicKey string) error {
	_, _, err := h.c.SSHKey.Create(ctx, hcloud.SSHKeyCreateOpts{Name: name, PublicKey: strings.TrimSpace(publicKey)})
	return err
}

// FirewallRule is one rule of a firewall as far as the tool sets rules.
type FirewallRule struct {
	Direction string // "in"
	Protocol  string // "tcp", "icmp"
	Port      string // "" for none
	SourceIPs []string
}

// FirewallRules returns the rules of the firewall of that name; exists is false if there is none.
func (h *Client) FirewallRules(ctx context.Context, name string) (rules []FirewallRule, exists bool, err error) {
	fw, _, err := h.c.Firewall.GetByName(ctx, name)
	if err != nil || fw == nil {
		return nil, false, err
	}
	for _, r := range fw.Rules {
		fr := FirewallRule{Direction: string(r.Direction), Protocol: string(r.Protocol)}
		if r.Port != nil {
			fr.Port = *r.Port
		}
		for _, n := range r.SourceIPs {
			fr.SourceIPs = append(fr.SourceIPs, n.String())
		}
		rules = append(rules, fr)
	}
	return rules, true, nil
}

// FirewallCreate creates an empty firewall.
func (h *Client) FirewallCreate(ctx context.Context, name string) error {
	res, _, err := h.c.Firewall.Create(ctx, hcloud.FirewallCreateOpts{Name: name})
	if err != nil {
		return err
	}
	return h.c.Action.WaitFor(ctx, res.Actions...)
}

// FirewallAddRule adds a rule to a firewall, keeping the others (the API sets all rules at once).
func (h *Client) FirewallAddRule(ctx context.Context, name string, r FirewallRule) error {
	fw, _, err := h.c.Firewall.GetByName(ctx, name)
	if err != nil {
		return err
	}
	if fw == nil {
		return fmt.Errorf("firewall %s not found", name)
	}
	rule := hcloud.FirewallRule{
		Direction: hcloud.FirewallRuleDirection(r.Direction),
		Protocol:  hcloud.FirewallRuleProtocol(r.Protocol),
	}
	if r.Port != "" {
		port := r.Port
		rule.Port = &port
	}
	for _, cidr := range r.SourceIPs {
		_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return fmt.Errorf("not a network: %s", cidr)
		}
		rule.SourceIPs = append(rule.SourceIPs, *n)
	}
	actions, _, err := h.c.Firewall.SetRules(ctx, fw, hcloud.FirewallSetRulesOpts{Rules: append(append([]hcloud.FirewallRule{}, fw.Rules...), rule)})
	if err != nil {
		return err
	}
	return h.c.Action.WaitFor(ctx, actions...)
}

// ServerSpec is what a new server is made from.
type ServerSpec struct {
	Name, Type, Location, Image string
	SSHKey                      string // name of a key in the project
	Firewall                    string // name of a firewall in the project
	UserData                    string
}

// ServerCreate creates a server and waits until it is running (hcloud server create).
func (h *Client) ServerCreate(ctx context.Context, s ServerSpec) (*Server, error) {
	st, _, err := h.c.ServerType.GetByName(ctx, s.Type)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("server type %s not found", s.Type)
	}
	// the image of the server type's architecture
	img, _, err := h.c.Image.GetByNameAndArchitecture(ctx, s.Image, st.Architecture)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, fmt.Errorf("image %s not found for %s", s.Image, st.Architecture)
	}
	fw, _, err := h.c.Firewall.GetByName(ctx, s.Firewall)
	if err != nil {
		return nil, err
	}
	if fw == nil {
		return nil, fmt.Errorf("firewall %s not found", s.Firewall)
	}
	// the API takes the ID of the key (a name only would be sent as ID 0)
	key, _, err := h.c.SSHKey.GetByName(ctx, s.SSHKey)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, fmt.Errorf("SSH key %s not found", s.SSHKey)
	}
	res, _, err := h.c.Server.Create(ctx, hcloud.ServerCreateOpts{
		Name:       s.Name,
		ServerType: st,
		Image:      img,
		Location:   &hcloud.Location{Name: s.Location},
		SSHKeys:    []*hcloud.SSHKey{key},
		Firewalls:  []*hcloud.ServerCreateFirewall{{Firewall: *fw}},
		UserData:   s.UserData,
	})
	if err != nil {
		return nil, err
	}
	if err := h.c.Action.WaitFor(ctx, append([]*hcloud.Action{res.Action}, res.NextActions...)...); err != nil {
		return nil, err
	}
	return serverFrom(res.Server), nil
}

func serverFrom(s *hcloud.Server) *Server {
	out := &Server{ID: s.ID, Name: s.Name, Created: s.Created}
	if s.PublicNet.IPv4.IP != nil {
		out.IPv4 = s.PublicNet.IPv4.IP.String()
	}
	return out
}
