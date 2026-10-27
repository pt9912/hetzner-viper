package model

// ProviderInfo is what viper knows of a cloud provider: which settings it needs and what its defaults are.
type ProviderInfo struct {
	// Credentials are the settings the provider's API needs (empty if it finds them itself, like
	// a credentials chain does).
	Credentials []string
	// Secrets are the environment variables that hold the provider's credentials, which programs
	// started from viper (VS Code) must not inherit.
	Secrets []string
	// Size, Region and Image are the defaults of SERVER_TYPE, SERVER_LOCATION and SERVER_IMAGE.
	Size, Region, Image string
}

// DefaultProvider is the provider when PROVIDER is not set.
const DefaultProvider = "hetzner"

// ProviderNames are the names PROVIDER takes.
func ProviderNames() []string { return []string{"aws", "hetzner"} }

// LookupProvider is what viper knows of the provider of that name; ok is false for an unknown one.
func LookupProvider(name string) (info ProviderInfo, ok bool) {
	switch name {
	case "hetzner":
		return ProviderInfo{Credentials: []string{"HCLOUD_TOKEN"}, Secrets: []string{"HCLOUD_TOKEN"}, Size: "cpx32", Region: "fsn1", Image: "ubuntu-26.04"}, true
	case "aws":
		// the credentials come from the AWS credentials chain (aws login, SSO, keys): none is required
		return ProviderInfo{Secrets: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}, Size: "t3.large", Region: "eu-central-1", Image: "ubuntu-24.04"}, true
	}
	return ProviderInfo{}, false
}
