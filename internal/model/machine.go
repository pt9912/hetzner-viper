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

	// Provider is the cloud the server is made in (PROVIDER).
	Provider string

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
