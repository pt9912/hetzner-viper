package model

// SecretNames are the settings whose values are credentials: those of the providers and the ones viper puts on the machine
// (editor/vscode.go keeps the first kind from VS Code the same way). One list: the redaction of the output, the page of the
// credentials and the backups of the settings files use it.
func SecretNames() []string {
	names := []string{"CLOUDFLARE_API_TOKEN", "VIPER_GH_TOKEN", "GH_TOKEN", "VIPER_CLAUDE_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "VIPER_ANTHROPIC_AUTH_TOKEN", "VIPER_CODE_SERVER_PASSWORD"}
	for _, p := range ProviderNames() {
		if info, ok := LookupProvider(p); ok {
			names = append(names, info.Secrets...)
		}
	}
	return names
}
