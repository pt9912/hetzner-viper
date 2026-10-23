// Package cloudflare manages the one DNS record of a machine (dns.sh): an A record that carries
// a comment marking it as ours. Records without that comment are never changed or deleted.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Comment marks the records this tool owns.
const Comment = "managed by hetzner-claude"

const defaultBase = "https://api.cloudflare.com/client/v4"

var ipv4Re = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

type Client struct {
	Token, ZoneID string
	Base          string       // default: the Cloudflare API
	HTTP          *http.Client // default: 30 s timeout
}

// Record is an A record as far as we use it.
type Record struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Comment string `json:"comment"`
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return defaultBase
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do sends one request and returns the "result" of a successful answer. The token goes only in
// the Authorization header, never into an error.
func (c *Client) do(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("Cloudflare API error: %w", urlErrorWithoutURL(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("Cloudflare API error: %w", err)
	}
	var out struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if resp.StatusCode >= 400 || json.Unmarshal(raw, &out) != nil || !out.Success {
		return nil, fmt.Errorf("Cloudflare API error: %s", strings.TrimSpace(string(raw)))
	}
	return out.Result, nil
}

func urlErrorWithoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// lookup returns our record for host, if any. More than one A record is an error: we would not
// know which one is ours.
func (c *Client) lookup(ctx context.Context, host string) (*Record, error) {
	q := url.Values{"type": {"A"}, "name": {host}, "per_page": {"100"}}
	raw, err := c.do(ctx, http.MethodGet, "/zones/"+c.ZoneID+"/dns_records?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("Cloudflare lookup of %s failed (%w)", host, err)
	}
	var recs []Record
	if err := json.Unmarshal(raw, &recs); err != nil {
		return nil, fmt.Errorf("Cloudflare lookup of %s failed (%w)", host, err)
	}
	switch len(recs) {
	case 0:
		return nil, nil
	case 1:
		return &recs[0], nil
	}
	return nil, errors.New("Multiple A records; refusing to modify")
}

func recordsPath(zone string) string { return "/zones/" + zone + "/dns_records" }

// Set points host at ip: creates the record, or updates ours; it never overwrites a record
// that is not ours.
func (c *Client) Set(ctx context.Context, host, ip string) (string, error) {
	rec, err := c.lookup(ctx, host)
	if err != nil {
		return "", err
	}
	if !ipv4Re.MatchString(ip) {
		return "", fmt.Errorf("not an IPv4 address: %s", ip)
	}
	payload := map[string]any{"type": "A", "name": host, "content": ip, "ttl": 60, "proxied": false, "comment": Comment}
	if rec != nil {
		if rec.Comment != Comment {
			return "", errors.New("Existing DNS record is not managed by this setup; refusing overwrite")
		}
		_, err = c.do(ctx, http.MethodPut, recordsPath(c.ZoneID)+"/"+rec.ID, payload)
	} else {
		_, err = c.do(ctx, http.MethodPost, recordsPath(c.ZoneID), payload)
	}
	if err != nil {
		return "", err
	}
	return host + " -> " + ip, nil
}

// Delete removes our record for host. With expectedIP it only does so while the record still
// points there. A record that is not ours, or no record at all, is not an error to fix here:
// the first is refused, the second is fine.
func (c *Client) Delete(ctx context.Context, host, expectedIP string) (string, error) {
	rec, err := c.lookup(ctx, host)
	if err != nil {
		return "", err
	}
	if rec != nil {
		if rec.Comment != Comment {
			return "", errors.New("DNS record not owned by setup; skipping")
		}
		if expectedIP != "" && rec.Content != expectedIP {
			return "", errors.New("DNS IP changed; refusing deletion")
		}
		if _, err := c.do(ctx, http.MethodDelete, recordsPath(c.ZoneID)+"/"+rec.ID, nil); err != nil {
			return "", err
		}
	}
	return "DNS record removed (if present)", nil
}

// Check is the read-only check of the preflight: the zone is readable and matches the domain,
// and the record is free or ours. It returns the lines to show; ok is false if one is a FAIL.
func (c *Client) Check(ctx context.Context, domain, host string) (lines []string, ok bool, err error) {
	rec, err := c.lookup(ctx, host)
	if err != nil {
		return nil, false, err
	}
	raw, zerr := c.do(ctx, http.MethodGet, "/zones/"+c.ZoneID, nil)
	var zone struct {
		Name string `json:"name"`
	}
	if zerr == nil {
		zerr = json.Unmarshal(raw, &zone)
	}
	if zerr != nil {
		return []string{fmt.Sprintf("FAIL  Cloudflare zone %s not readable with this token", c.ZoneID)}, false, nil
	}
	if domain != zone.Name && !strings.HasSuffix(domain, "."+zone.Name) {
		return []string{fmt.Sprintf("FAIL  DNS_DOMAIN %s is not in Cloudflare zone %s", domain, zone.Name)}, false, nil
	}
	lines = append(lines, fmt.Sprintf("ok    Cloudflare zone %s readable (edit permission is only seen when viper up writes the record)", zone.Name))
	switch {
	case rec == nil:
		lines = append(lines, fmt.Sprintf("ok    %s: no record yet", host))
	case rec.Comment == Comment:
		lines = append(lines, fmt.Sprintf("info  %s -> %s (managed by this setup)", host, rec.Content))
	default:
		lines = append(lines, fmt.Sprintf("FAIL  %s already has a record not managed by this setup", host))
		return lines, false, nil
	}
	return lines, true, nil
}
