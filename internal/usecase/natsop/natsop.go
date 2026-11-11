// Package natsop registers the operator of a NATS group at its server (docs/plan/nats-cli.md, stage 2).
package natsop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/natsgroup"
)

// Service is the use case; it implements driving.NATSOperator.
type Service struct{ Ops natsgroup.Ops }

func target(m driving.NATSMachine) natsgroup.Target {
	return natsgroup.Target{Config: m.Target.Config, Alias: m.Target.Alias}
}

// Find asks the machines in turn which of them holds the registry of the server.
func (s Service) Find(ctx context.Context, machines []driving.NATSMachine) (driving.NATSServerInfo, error) {
	var lastErr error
	for _, m := range machines {
		clients, end, ok, err := s.Ops.Registry(ctx, target(m))
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", m.Name, err)
			continue
		}
		if !ok {
			continue
		}
		ca, err := s.Ops.Certificate(ctx, target(m))
		if err != nil {
			return driving.NATSServerInfo{}, fmt.Errorf("%s: reading the certificate: %w", m.Name, err)
		}
		if !strings.Contains(ca, "BEGIN CERTIFICATE") {
			return driving.NATSServerInfo{}, fmt.Errorf("%s: the server has no certificate", m.Name)
		}
		info := driving.NATSServerInfo{Machine: m, Host: end.Host, Port: end.Port, CA: ca}
		for _, c := range clients {
			if c.Operator {
				info.Operators = append(info.Operators, c.Machine)
			}
		}
		return info, nil
	}
	if lastErr != nil {
		return driving.NATSServerInfo{}, fmt.Errorf("the server of the group was not found; the last machine said: %w", lastErr)
	}
	return driving.NATSServerInfo{}, errors.New("none of the machines is the NATS server of the group (viper up starts it)")
}

// Register enters the operator at the server.
func (s Service) Register(ctx context.Context, group string, server driving.NATSServerInfo, name, publicKey string) error {
	add := func(l []natsserver.Client) ([]natsserver.Client, error) {
		return natsserver.WithEntry(l, natsserver.Client{Machine: name, PublicKey: publicKey, Operator: true})
	}
	return s.Ops.Update(ctx, target(server.Machine), group, natsserver.Endpoint{Host: server.Host, Port: server.Port}, add)
}

// Revoke removes the operator from the server.
func (s Service) Revoke(ctx context.Context, group string, server driving.NATSServerInfo, name string) error {
	drop := func(l []natsserver.Client) ([]natsserver.Client, error) {
		out := natsserver.WithoutOperator(l, name)
		if len(out) == len(l) {
			return nil, natsgroup.ErrNoChange
		}
		return out, nil
	}
	return s.Ops.Update(ctx, target(server.Machine), group, natsserver.Endpoint{Host: server.Host, Port: server.Port}, drop)
}
