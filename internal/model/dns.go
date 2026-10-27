package model

// The values of DNS_PROVIDER.
const (
	// DNSCloudflare sets an A record in a Cloudflare zone (the default).
	DNSCloudflare = "cloudflare"
	// DNSNone sets no record: the machine is reached by its address (viper ssh), or by the name its
	// cloud gives it, if it gives one.
	DNSNone = "none"
	// NoDNSDomain is the domain of a machine without DNS: a reserved name (RFC 2606) that only
	// identifies the server in its host certificate and ssh config.
	NoDNSDomain = "viper.invalid"
)

// HasDNS reports whether viper sets a DNS record for the machine.
func (m *Machine) HasDNS() bool { return m.DNSProvider != DNSNone }
