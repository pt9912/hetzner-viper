// Package hetzner is the part of the Hetzner Cloud API the tool uses, over the official client.
package hetzner

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Client talks to the Hetzner Cloud API with a project token.
type Client struct {
	c *hcloud.Client
}

// New returns a client. endpoint is for tests; "" is the real API.
func New(token, endpoint string) *Client {
	opts := []hcloud.ClientOption{hcloud.WithToken(token), hcloud.WithApplication("hetzner-viper", "")}
	if endpoint != "" {
		opts = append(opts, hcloud.WithEndpoint(endpoint))
	}
	opts = append(opts, hcloud.WithHTTPClient(&http.Client{Timeout: 60 * time.Second}))
	return &Client{c: hcloud.NewClient(opts...)}
}

// ServerTypeLocation says whether a server type can be ordered in a location.
type ServerTypeLocation struct {
	Name             string
	Available        bool
	UnavailableAfter time.Time // zero if the type is not deprecated there
}

// ServerType is what the preflight shows and checks of a server type.
type ServerType struct {
	Name         string
	Cores        int
	MemoryGB     float32
	DiskGB       int
	Architecture string // "x86" or "arm"
	Locations    []ServerTypeLocation
}

// SSHKey is a public key stored in the project.
type SSHKey struct {
	Name        string
	Fingerprint string // MD5, colon-separated
}

// ServerNames lists the names of all servers in the project: a cheap read that shows whether
// the token, the project and the network work.
func (h *Client) ServerNames(ctx context.Context) ([]string, error) {
	servers, err := h.c.Server.All(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return names, nil
}

// ServerType returns the server type of that name, nil if there is none.
func (h *Client) ServerType(ctx context.Context, name string) (*ServerType, error) {
	t, _, err := h.c.ServerType.GetByName(ctx, name)
	if err != nil || t == nil {
		return nil, err
	}
	out := &ServerType{Name: t.Name, Cores: t.Cores, MemoryGB: t.Memory, DiskGB: t.Disk, Architecture: string(t.Architecture)}
	if out.Architecture == "" {
		out.Architecture = "x86"
	}
	for _, l := range t.Locations {
		loc := ServerTypeLocation{Available: l.Available}
		if l.Location != nil {
			loc.Name = l.Location.Name
		}
		if l.Deprecation != nil {
			loc.UnavailableAfter = l.Deprecation.UnavailableAfter
		}
		out.Locations = append(out.Locations, loc)
	}
	return out, nil
}

// Server is a server of the project as far as the commands use it.
type Server struct {
	ID   int64
	Name string
	IPv4 string
}

// ServerByName returns the server of that name, nil if there is none.
func (h *Client) ServerByName(ctx context.Context, name string) (*Server, error) {
	s, _, err := h.c.Server.GetByName(ctx, name)
	if err != nil || s == nil {
		return nil, err
	}
	out := &Server{ID: s.ID, Name: s.Name}
	if s.PublicNet.IPv4.IP != nil {
		out.IPv4 = s.PublicNet.IPv4.IP.String()
	}
	return out, nil
}

// LocationExists reports whether a location of that name exists.
func (h *Client) LocationExists(ctx context.Context, name string) (bool, error) {
	l, _, err := h.c.Location.GetByName(ctx, name)
	return l != nil, err
}

// ImageExists reports whether an image of that name exists for the architecture.
func (h *Client) ImageExists(ctx context.Context, name, architecture string) (bool, error) {
	img, _, err := h.c.Image.GetByNameAndArchitecture(ctx, name, hcloud.Architecture(architecture))
	return img != nil, err
}

// SSHKeys lists the public keys of the project.
func (h *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	keys, err := h.c.SSHKey.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SSHKey, len(keys))
	for i, k := range keys {
		out[i] = SSHKey{Name: k.Name, Fingerprint: k.Fingerprint}
	}
	return out, nil
}

// Error is an API error without the request details, safe to show.
func Error(err error) string {
	if err == nil {
		return ""
	}
	if hcloud.IsError(err, hcloud.ErrorCodeUnauthorized) || strings.Contains(err.Error(), "status code 401") {
		return "unauthorized (check HCLOUD_TOKEN)"
	}
	return strings.TrimSpace(fmt.Sprint(err))
}
