package hetzner

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

var _ cloud.NATSProvider = (*Client)(nil)

// ListGroupMachines returns the servers of the project that carry the NATS group as a label and
// are marked as managed by viper. Servers of other projects or groups are never returned; a
// server with NATS labels that make no sense is an error, so nobody guesses about the group.
func (h *Client) ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	sel := fmt.Sprintf("%s=true,%s=%s", model.LabelManaged, model.LabelNATSGroup, group)
	servers, err := h.c.Server.AllWithOpts(ctx, hcloud.ServerListOpts{ListOpts: hcloud.ListOpts{LabelSelector: sel}})
	if err != nil {
		return nil, err
	}
	out := make([]model.GroupMember, 0, len(servers))
	for _, s := range servers {
		meta, ok, err := model.NATSMetadataFromLabels(s.Labels)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", s.Name, err)
		}
		if !ok || meta.Group != group {
			continue
		}
		out = append(out, model.GroupMember{Server: *serverFrom(s), NATS: meta})
	}
	return out, nil
}

// UpdateNATSMetadata sets the NATS labels of the server. Its other labels stay: the API replaces
// all labels on an update, so the current ones are read and merged first.
func (h *Client) UpdateNATSMetadata(ctx context.Context, serverID string, meta model.NATSMetadata) error {
	id, err := strconv.ParseInt(serverID, 10, 64)
	if err != nil {
		return fmt.Errorf("not a server ID: %s", serverID)
	}
	if err := model.CheckNATSName("NATS group", meta.Group); err != nil {
		return err
	}
	s, _, err := h.c.Server.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("server %s not found", serverID)
	}
	labels := map[string]string{}
	for k, v := range s.Labels {
		labels[k] = v
	}
	for k, v := range model.NATSLabels(meta) {
		labels[k] = v
	}
	_, _, err = h.c.Server.Update(ctx, s, hcloud.ServerUpdateOpts{Labels: labels})
	return err
}

// NATSMetadataOf returns the NATS labels of the server, nil if it has none.
func (h *Client) NATSMetadataOf(ctx context.Context, serverID string) (*model.NATSMetadata, error) {
	id, err := strconv.ParseInt(serverID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("not a server ID: %s", serverID)
	}
	s, _, err := h.c.Server.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}
	meta, ok, err := model.NATSMetadataFromLabels(s.Labels)
	if err != nil || !ok {
		return nil, err
	}
	return &meta, nil
}
