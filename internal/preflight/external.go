package preflight

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/config"
)

func (d Deps) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// netError is an error of a request without the URL, which may hold credentials.
func netError(err error) string {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err.Error()
	}
	return err.Error()
}

func githubChecks(ctx context.Context, r *report, c *config.Config, d Deps, repoURL string) {
	fmt.Fprintln(r.out, "== GitHub")
	token := c.Get("VIPER_GH_TOKEN")
	if token == "" {
		r.info("no VIPER_GH_TOKEN: gh is not logged in on the server")
		return
	}
	host := c.Setting("VIPER_GH_HOST", "github.com")
	api := "https://api.github.com"
	if host != "github.com" {
		api = "https://" + host + "/api/v3"
	}
	// The token goes in a header only, so it never appears in a URL or an error.
	get := func(path string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return d.client().Do(req)
	}
	resp, err := get("/user")
	if err != nil {
		r.bad("VIPER_GH_TOKEN rejected by %s: %s", host, netError(err))
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		r.bad("VIPER_GH_TOKEN rejected by %s: HTTP %d", host, resp.StatusCode)
		return
	}
	var user struct {
		Login string `json:"login"`
	}
	_ = json.Unmarshal(body, &user)
	r.ok("VIPER_GH_TOKEN valid for %s on %s", user.Login, host)
	if scopes := strings.TrimSpace(resp.Header.Get("X-OAuth-Scopes")); scopes != "" {
		// gh's minimum: repo, and read:org or a broader org scope (write:org, admin:org)
		have := "," + strings.ReplaceAll(scopes, " ", "") + ","
		if !strings.Contains(have, ",repo,") {
			r.bad("classic token lacks scope repo (has: %s); gh auth login will reject it", scopes)
		}
		if !strings.Contains(have, ",read:org,") && !strings.Contains(have, ",write:org,") && !strings.Contains(have, ",admin:org,") {
			r.bad("classic token lacks scope read:org (or write:org/admin:org) (has: %s); gh auth login will reject it", scopes)
		}
	}
	// an SSH URL with a deploy key is cloned with that key, not with the token
	re := regexp.MustCompile(`^(https://` + regexp.QuoteMeta(host) + `/|git@` + regexp.QuoteMeta(host) + `:)([^/]+)/([^/]+)$`)
	m := re.FindStringSubmatch(repoURL)
	if repoURL == "" || m == nil || (c.Get("GIT_DEPLOY_KEY_FILE") != "" && !strings.HasPrefix(repoURL, "https://")) {
		return
	}
	repo := m[2] + "/" + strings.TrimSuffix(m[3], ".git")
	access, err := get("/repos/" + repo)
	if err == nil {
		access.Body.Close()
	}
	if err != nil || access.StatusCode >= 400 {
		r.bad("token cannot access %s (fine-grained token without this repository?)", repo)
		return
	}
	r.ok("token can access %s", repo)
	// the first step of a git push: the ref list of git-receive-pack needs write access
	// (fine-grained: Contents read and write); nothing is written
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/"+repo+".git/info/refs?service=git-receive-pack", nil)
	if err != nil {
		r.warn("push permission for %s not checked (HTTP error)", repo)
		return
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Login+":"+token)))
	push, err := d.client().Do(req)
	if err != nil {
		r.warn("push permission for %s not checked (HTTP error)", repo)
		return
	}
	push.Body.Close()
	switch push.StatusCode {
	case 200:
		r.ok("token can push to %s", repo)
	case 401, 403, 404:
		r.bad("token cannot push to %s (fine-grained token: Contents must be Read and write)", repo)
	default:
		r.warn("push permission for %s not checked (HTTP %d)", repo, push.StatusCode)
	}
}

var (
	numberRe = regexp.MustCompile(`^[0-9]+$`)
	httpRe   = regexp.MustCompile(`^https?://`)
)

func claudeChecks(ctx context.Context, r *report, c *config.Config, d Deps) {
	fmt.Fprintln(r.out, "== Claude Code")
	baseURL, token := c.Get("VIPER_ANTHROPIC_BASE_URL"), c.Get("VIPER_CLAUDE_TOKEN")
	switch {
	case baseURL != "":
		gatewayChecks(ctx, r, c, d, baseURL, token)
	case token != "":
		if strings.IndexFunc(token, func(r rune) bool { return r == ' ' || (r >= '\t' && r <= '\r') }) >= 0 {
			r.bad("VIPER_CLAUDE_TOKEN contains whitespace (copied with a line break?)")
		} else {
			r.ok("VIPER_CLAUDE_TOKEN set: Claude Code will be logged in on the server (validity is only seen when claude runs)")
		}
	default:
		r.info("no VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL: log in on the server with claude (/login) after each new server")
	}
}

func gatewayChecks(ctx context.Context, r *report, c *config.Config, d Deps, baseURL, claudeToken string) {
	if claudeToken != "" {
		r.bad("set either VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, not both (the API token would take precedence over the subscription)")
	}
	authToken, model := c.Get("VIPER_ANTHROPIC_AUTH_TOKEN"), c.Get("VIPER_CLAUDE_MODEL")
	for _, v := range []string{"VIPER_ANTHROPIC_AUTH_TOKEN", "VIPER_CLAUDE_MODEL"} {
		if c.Get(v) == "" {
			r.bad("%s is required with VIPER_ANTHROPIC_BASE_URL", v)
		}
	}
	if n := c.Get("VIPER_CLAUDE_MAX_CONTEXT_TOKENS"); n != "" && !numberRe.MatchString(n) {
		r.bad("VIPER_CLAUDE_MAX_CONTEXT_TOKENS must be a number of tokens")
	}
	base := strings.TrimSuffix(baseURL, "/")
	shown := RedactURL(base)
	if !httpRe.MatchString(base) {
		r.bad("VIPER_ANTHROPIC_BASE_URL must start with https:// (or http://): %s", shown)
		return
	}
	if authToken == "" || model == "" {
		return
	}
	if !strings.HasPrefix(base, "https://") {
		r.warn("VIPER_ANTHROPIC_BASE_URL is not https: prompts and the token travel unencrypted")
	}
	// the request Claude Code makes (Bearer token, Messages API); costs a few tokens.
	// A suffix like [1m] is a Claude Code marker, not part of the model ID.
	modelID, _, _ := strings.Cut(model, "[")
	payload, _ := json.Marshal(map[string]any{
		"model": modelID, "max_tokens": 16,
		"messages": []map[string]string{{"role": "user", "content": "Reply with exactly: OK"}},
	})
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		r.bad("%s not reachable: %s", shown, netError(err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client().Do(req)
	if err != nil {
		r.bad("%s not reachable: %s", shown, strings.ReplaceAll(netError(err), authToken, "***"))
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	msg := errorMessage(raw)
	with := ""
	if msg != "" {
		with = ": " + msg
	}
	switch resp.StatusCode {
	case 200:
		r.ok("%s answers with model %s (Anthropic Messages API)", shown, modelID)
	case 401, 403:
		r.bad("%s rejects VIPER_ANTHROPIC_AUTH_TOKEN (HTTP %d%s)", shown, resp.StatusCode, with)
	case 404:
		r.bad("%s: model %s or the Messages API not found (HTTP 404%s)", shown, modelID, with)
	default:
		r.bad("%s: HTTP %d%s", shown, resp.StatusCode, with)
	}
}

// errorMessage is .error.message, else .error, else .message of an error answer (200 bytes at most).
func errorMessage(raw []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	text := func(m json.RawMessage) string {
		if len(m) == 0 || string(m) == "null" || string(m) == "false" {
			return ""
		}
		var s string
		if json.Unmarshal(m, &s) == nil {
			return s
		}
		return string(m)
	}
	var inner struct {
		Message json.RawMessage `json:"message"`
	}
	msg := ""
	if json.Unmarshal(v.Error, &inner) == nil {
		msg = text(inner.Message)
	}
	if msg == "" {
		msg = text(v.Error)
	}
	if msg == "" {
		msg = text(v.Message)
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
