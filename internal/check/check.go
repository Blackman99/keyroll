// Package check asks each provider, with one free read-only request, whether a
// key still works. It only ever talks to the provider that issued the key, on
// that provider's official API host, with the key in a header.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Blackman99/keyroll/internal/provider"
)

// State is what the provider said.
type State string

const (
	Alive    State = "alive"    // the provider accepted the key
	Rejected State = "rejected" // the provider refuses the key: revoked, deleted or mistyped (they all answer the same)
	Unknown  State = "unknown"  // network error, 5xx, or an answer we cannot read
	Skipped  State = "skipped"  // not checked (no endpoint, login token, unknown issuer)
)

// Result of one check.
type Result struct {
	State    State    `json:"state"`
	HTTP     int      `json:"http,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	Endpoint string   `json:"endpoint,omitempty"`
	Limit    *float64 `json:"limit_usd,omitempty"` // OpenRouter: spend limit in USD (nil = no cap)
	Usage    *float64 `json:"usage_usd,omitempty"` // OpenRouter: usage so far in USD
	NoCap    bool     `json:"no_cap,omitempty"`    // provider reports no spend limit on this key
	MS       int64    `json:"ms,omitempty"`
}

// Client performs checks.
type Client struct {
	HTTP      *http.Client
	Override  map[string]string // provider id → URL (tests)
	UserAgent string
	Parallel  int
}

// New returns a client with a 10 s timeout that never follows redirects
// (a redirect must not carry the key header to another host).
func New(version string) *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		UserAgent: "keyroll/" + version + " (+https://github.com/Blackman99/keyroll)",
		Parallel:  4,
	}
}

// Job is one key to check.
type Job struct {
	Provider *provider.Provider
	Kind     provider.Kind
	Secret   string
}

// All checks jobs concurrently and returns results in the same order.
func (c *Client) All(ctx context.Context, jobs []Job) []Result {
	out := make([]Result, len(jobs))
	sem := make(chan struct{}, max(1, c.Parallel))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = c.One(ctx, j.Provider, j.Kind, j.Secret)
		}(i, j)
	}
	wg.Wait()
	return out
}

// Skip explains why a key is not checked, or "" if it can be.
func Skip(p *provider.Provider, kind provider.Kind) string {
	switch {
	case p == nil || p.ID == provider.Unknown.ID:
		return "issuer unknown: keyroll does not guess where to send a key"
	case kind == provider.KindLogin:
		return "login token managed by the tool: log out and in again instead"
	case kind == provider.KindAdmin:
		return "admin key: cannot call model endpoints; check it in the console"
	case p.CheckURL == "":
		return p.Name + " has no free read-only endpoint; check it in the dashboard"
	}
	return ""
}

// One checks a single key.
func (c *Client) One(ctx context.Context, p *provider.Provider, kind provider.Kind, secret string) Result {
	if why := Skip(p, kind); why != "" {
		return Result{State: Skipped, Detail: why}
	}
	url := p.CheckURL
	if o, ok := c.Override[p.ID]; ok {
		url = o
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{State: Unknown, Detail: err.Error()}
	}
	switch p.Header {
	case "":
		req.Header.Set("Authorization", "Bearer "+secret)
	default:
		req.Header.Set(p.Header, secret)
	}
	if p.ID == "anthropic" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	t0 := time.Now()
	resp, err := c.HTTP.Do(req)
	ms := time.Since(t0).Milliseconds()
	endpoint := strings.SplitN(p.CheckURL, "?", 2)[0]
	if err != nil {
		return Result{State: Unknown, Detail: netReason(err, c.HTTP.Timeout), Endpoint: endpoint, MS: ms}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	r := Interpret(p.ID, resp.StatusCode, body)
	r.Endpoint, r.MS = endpoint, ms
	return r
}

// Interpret turns a provider answer into a State. It is pure so the real
// answers recorded in testdata/ can be replayed.
func Interpret(id string, status int, body []byte) Result {
	r := Result{HTTP: status}
	low := strings.ToLower(string(body))
	switch {
	case status >= 200 && status < 300:
		r.State, r.Detail = Alive, fmt.Sprintf("accepted (%d)", status)
		switch id {
		case "openrouter":
			var v struct {
				Data struct {
					Limit *float64 `json:"limit"`
					Usage *float64 `json:"usage"`
				} `json:"data"`
			}
			if json.Unmarshal(body, &v) == nil {
				r.Limit, r.Usage = v.Data.Limit, v.Data.Usage
				r.NoCap = v.Data.Limit == nil
				if r.NoCap {
					r.Detail = "no spend limit on this key"
				} else {
					r.Detail = fmt.Sprintf("limit $%.2f", *v.Data.Limit)
				}
				if v.Data.Usage != nil {
					r.Detail += fmt.Sprintf(", used $%.2f", *v.Data.Usage)
				}
			}
		case "xai":
			var v struct {
				Blocked  bool `json:"api_key_blocked"`
				Disabled bool `json:"api_key_disabled"`
				Team     bool `json:"team_blocked"`
			}
			if json.Unmarshal(body, &v) == nil && (v.Blocked || v.Disabled || v.Team) {
				r.State, r.Detail = Rejected, "xAI reports this key blocked or disabled"
			}
		case "deepseek":
			var v struct {
				Available bool `json:"is_available"`
			}
			if json.Unmarshal(body, &v) == nil && !v.Available {
				r.Detail = "no balance left"
			}
		}
	case status == 401:
		r.State, r.Detail = Rejected, "refused, HTTP 401"
	case status == 400 && (strings.Contains(low, "api_key_invalid") || strings.Contains(low, "api key not valid") || strings.Contains(low, "incorrect api key")):
		// Google answers 400 INVALID_ARGUMENT/API_KEY_INVALID, xAI 400 invalid-argument
		r.State, r.Detail = Rejected, fmt.Sprintf("refused, HTTP %d API_KEY_INVALID", status)
	case status == 403 && (strings.Contains(low, "service_disabled") || strings.Contains(low, "has not been used in project") || strings.Contains(low, "api_key_service_blocked")):
		r.State, r.Detail = Alive, "key works, but the Gemini API is not enabled on its Google project"
	case status == 403:
		r.State, r.Detail = Unknown, "forbidden (403): key may be valid but restricted (IP / region / org)"
	case status == 429 && (strings.Contains(low, "insufficient_quota") || strings.Contains(low, "credit") || strings.Contains(low, "billing") || strings.Contains(low, "balance")):
		r.State, r.Detail = Alive, "accepted, but out of credit (429)"
	case status == 429:
		// A bare 429 can come before authentication (per-IP limits), so it
		// proves nothing about the key.
		r.State, r.Detail = Unknown, "rate-limited (429) before answering; try again in a minute"
	case status == 402:
		r.State, r.Detail = Alive, "accepted, but out of credit (402)"
	case status >= 500:
		r.State, r.Detail = Unknown, fmt.Sprintf("provider error (%d); try again", status)
	default:
		r.State, r.Detail = Unknown, fmt.Sprintf("unexpected answer (%d)", status)
	}
	return r
}

func netReason(err error, timeout time.Duration) string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		return "DNS lookup failed (offline?)"
	case errors.As(err, &ne) && ne.Timeout():
		return fmt.Sprintf("no answer within %s (raise --timeout)", timeout)
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused"
	case strings.Contains(err.Error(), "certificate"):
		return "TLS certificate problem (proxy intercepting HTTPS?)"
	}
	return "network error"
}
