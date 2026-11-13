package model

import (
	"path"
	"strings"
)

// Machine is one machine as the settings describe it: what server it is, where it is made, and the
// names and paths that follow from that. The settings files are read by internal/config.
type Machine struct {
	Name string // the machine's name, as typed after the command
	Dir  string // where the settings are

	// Vars looks a setting up (nil: none); see Get.
	Vars func(name string) string

	// Provider is the cloud the server is made in (PROVIDER). For a Kubernetes cluster the server is a container (see IsContainer).
	Provider string

	// NATS is the NATS group of the machine (NATS_ENABLED, NATS_GROUP, NATS_PORT); the zero value
	// is off.
	NATS NATSConfig

	// Group is the group of groups/<name>.env this machine is named in ("" if none); GroupFile is that
	// file, and Warnings are what the loading of the settings found that does not stop it (a file of
	// another group that cannot be read).
	Group, GroupFile string
	// GroupChosen is true when other groups name the machine too, so that the settings of Group were chosen (--in-group)
	// and anything that loads the settings later (the ProxyCommand of the ssh config) has to be told which.
	GroupChosen bool
	Warnings    []string

	// DNSProvider is who makes the DNS name of the server: DNSCloudflare, or DNSNone for no name.
	DNSProvider string

	DNSDomain, ServerType, ServerLocation, ServerImage, ServerPrefix, WorkspaceDir string
	SSHKey                                                                         string

	ServerName, SSHAlias, DNSHost, KeyName, FirewallName string
	StateDir, SSHDir, SSHConfig, KnownPin                string

	// state files of the machine, all keyed by the server name
	IPState, IDState, WorkspaceState, ReadyState, RepoState string
	EventsLog, LogDir                                       string
}

// Get returns a setting, "" if it is not set.
func (m *Machine) Get(name string) string {
	if m.Vars == nil {
		return ""
	}
	return m.Vars(name)
}

// Setting is a setting with a default for when it is unset or empty (${NAME:-default}).
func (m *Machine) Setting(name, def string) string {
	if v := m.Get(name); v != "" {
		return v
	}
	return def
}

// WorkDir is where the repository is on the server: the workspace directory, and below it the
// repository's name (the last part of its URL without .git).
func (m *Machine) WorkDir(repoURL string) string {
	if repoURL == "" {
		return m.WorkspaceDir
	}
	return m.WorkspaceDir + "/" + path.Base(strings.TrimSuffix(repoURL, ".git"))
}

// ProxyVerb is the command of viper that is the ProxyCommand of the ssh config of a container.
func (m *Machine) ProxyVerb() string {
	if m.Provider == "compose" {
		return "docker-proxy"
	}
	return "k8s-proxy"
}

// NATSServerHost is the name the clients of the NATS group connect to, and what the certificate of the server is for: in a
// cluster the headless Service of the group in its namespace, on a Docker host the alias of the server on the network of
// the group. The address of a container changes, the name does not.
func (m *Machine) NATSServerHost() string {
	if m.Provider == "compose" {
		return "viper-nats-" + m.NATS.Group
	}
	return NATSServiceHost(m.NATS.Group, m.Setting("K8S_NAMESPACE", "viper"))
}

// DockerMode is how a container gets its Docker (dind, remote or none).
func (m *Machine) DockerMode() string {
	if m.Provider == "compose" {
		return m.Setting("COMPOSE_DOCKER", "dind")
	}
	return m.Setting("K8S_DOCKER", "dind")
}

// Where says in words where a container is, for the question before viper down.
func (m *Machine) Where() string {
	if m.Provider == "compose" {
		return "container, Docker context " + m.ServerLocation
	}
	return "pod, " + m.Setting("K8S_NAMESPACE", "viper")
}

// DockerLogHint says where to look when Docker does not answer in a container.
func (m *Machine) DockerLogHint() string {
	if m.Provider == "compose" {
		return "docker --context " + m.ServerLocation + " logs " + m.ServerName + "-docker"
	}
	return "kubectl -n " + m.Setting("K8S_NAMESPACE", "viper") + " logs " + m.ServerName + "-0 -c dockerd"
}

// IsContainer tells whether the machine is a container (PROVIDER=kubernetes or compose) and not a VM.
func (m *Machine) IsContainer() bool {
	info, _ := LookupProvider(m.Provider)
	return info.Container
}
