package editor_test

import (
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/editor"
)

func TestVSCodeDoesNotInheritTheTokens(t *testing.T) {
	env := editor.Env([]string{"PATH=/bin", "HCLOUD_TOKEN=a", "CLOUDFLARE_API_TOKEN=b", "CLOUDFLARE_ZONE_ID=z", "VIPER_GH_TOKEN=c", "GH_TOKEN=d",
		"VIPER_CLAUDE_TOKEN=e", "CLAUDE_CODE_OAUTH_TOKEN=f", "VIPER_ANTHROPIC_AUTH_TOKEN=g", "VIPER_CODE_SERVER_PASSWORD=h", "VIPER_TRANSCRIPT=x",
		"HOME=/home/me", "VIPER_SWAP=1", "TOKEN_LIKE=keep", "HCLOUD_TOKEN=x", "AWS_SECRET_ACCESS_KEY=y"}, []string{"HCLOUD_TOKEN", "AWS_SECRET_ACCESS_KEY"})
	if strings.Join(env, " ") != "PATH=/bin HOME=/home/me VIPER_SWAP=1 TOKEN_LIKE=keep" {
		t.Errorf("%q", env)
	}
}
