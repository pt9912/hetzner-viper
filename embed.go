// Package hetznerviper carries the files the viper binary needs without a checkout.
package hetznerviper

import _ "embed"

// EnvExample is the template for .env.local.
//
//go:embed .env.local.example
var EnvExample []byte

// CloudInit is the cloud-init configuration every new server gets (the user-data is made from it).
//
//go:embed cloud-init.yaml
var CloudInit []byte
