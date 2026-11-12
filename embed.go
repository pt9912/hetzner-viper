// Package hetznerviper carries the files the viper binary needs without a checkout.
package hetznerviper

import "embed"

// EnvExample is the template for .env.local.
//
//go:embed .env.local.example
var EnvExample []byte

// MachineExample is the template for machines/<machine>.env.local.
//
//go:embed machines/example.env.local.example
var MachineExample []byte

// GroupExample is the template for groups/<group>.env.
//
//go:embed groups/example.env.example
var GroupExample []byte

// CloudInit is the cloud-init configuration every new server gets (the user-data is made from it).
//
//go:embed cloud-init.yaml
var CloudInit []byte

// ImageContext are the files that build the image of the Kubernetes provider, besides cloud-init.yaml (viper image build):
// the user of a downloaded viper has neither a checkout nor make.
//
//go:embed image/Dockerfile image/entry.sh image/sshd_config image/seed.py image/nats-supervisor.sh
var ImageContext embed.FS
