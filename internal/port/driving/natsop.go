package driving

import "context"

// NATSTarget is a machine that viper reaches by ssh: the file with its ssh config and its alias in it.
type NATSTarget struct{ Config, Alias string }

// NATSMachine is a machine of a NATS group and how to reach it.
type NATSMachine struct {
	Name   string
	Target NATSTarget
}

// NATSServerInfo is the server of a NATS group: the machine, where it listens and its certificate.
type NATSServerInfo struct {
	Machine NATSMachine
	Host    string // the private address, which the certificate is for
	Port    int
	CA      string // the certificate, PEM; the clients trust exactly this one
	// Operators are the operator entries the server has.
	Operators []string
}

// NATSOperator is the identity of the person who runs viper in a NATS group (docs/plan/nats-cli.md,
// stage 2): an entry at the server with its own name and the rights of OperatorPermissions.
type NATSOperator interface {
	// Find returns the server of the group among the machines: the one that holds the list of the
	// registered ones. An error if none of them is it.
	Find(ctx context.Context, machines []NATSMachine) (NATSServerInfo, error)
	// Register enters the operator at the server (its key is replaced if the name is the operator's
	// already) and has the server read it. An error if a machine has the name.
	Register(ctx context.Context, group string, server NATSServerInfo, name, publicKey string) error
	// Revoke removes the operator from the server; one that is not there is no error.
	Revoke(ctx context.Context, group string, server NATSServerInfo, name string) error
}
