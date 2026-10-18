// Package provider knows what AI provider API keys look like, where to ask
// whether one still works, and where to revoke it.
package provider

import (
	"regexp"
	"strings"
)

// Kind separates keys you rotate from tokens a tool refreshes by itself.
type Kind string

const (
	KindAPI   Kind = "api"   // a long-lived API key: rotate it
	KindAdmin Kind = "admin" // an org admin key: rotate it, but it cannot call model endpoints
	KindLogin Kind = "login" // an OAuth/session token a tool manages: log out and in again instead
)

// Provider describes one API key issuer.
type Provider struct {
	ID        string // stable id used in JSON and on the command line
	Name      string // display name
	CheckURL  string // free, read-only endpoint; "" means keyroll cannot check this provider
	Header    string // header that carries the key ("" = Authorization: Bearer)
	KeysURL   string // where to revoke / create keys
	CapURL    string // where to set a spend limit ("" = same as KeysURL)
	aliases   []string
	patterns  []pattern
	ctxValues *regexp.Regexp // value shape accepted only when the surrounding text names this provider
}

type pattern struct {
	re   *regexp.Regexp
	kind Kind
}

// Unknown is used for sk-style keys whose issuer cannot be told from the key or
// its surroundings. keyroll maps them but never sends them anywhere.
var Unknown = &Provider{ID: "unknown", Name: "unknown issuer"}

func p(re string, k Kind) pattern { return pattern{regexp.MustCompile(re), k} }

// All is the provider table, most specific prefixes first.
var All = []*Provider{
	{ID: "anthropic", Name: "Anthropic", CheckURL: "https://api.anthropic.com/v1/models?limit=1", Header: "x-api-key",
		KeysURL: "https://console.anthropic.com/settings/keys", CapURL: "https://console.anthropic.com/settings/limits",
		aliases: []string{"anthropic", "claude"},
		patterns: []pattern{
			p(`sk-ant-api\d{2}-[A-Za-z0-9_\-]{20,}`, KindAPI),
			p(`sk-ant-admin\d{2}-[A-Za-z0-9_\-]{20,}`, KindAdmin),
			p(`sk-ant-(?:oat|ort)\d{2}-[A-Za-z0-9_\-]{20,}`, KindLogin),
		}},
	{ID: "openrouter", Name: "OpenRouter", CheckURL: "https://openrouter.ai/api/v1/key",
		KeysURL:  "https://openrouter.ai/settings/keys",
		aliases:  []string{"openrouter"},
		patterns: []pattern{p(`sk-or-v1-[0-9a-f]{64}`, KindAPI)}},
	{ID: "openai", Name: "OpenAI", CheckURL: "https://api.openai.com/v1/models",
		KeysURL: "https://platform.openai.com/api-keys", CapURL: "https://platform.openai.com/settings/organization/limits",
		aliases: []string{"openai", "codex"},
		patterns: []pattern{
			p(`sk-(?:proj|svcacct)-[A-Za-z0-9_\-]{20,}`, KindAPI),
			p(`sk-admin-[A-Za-z0-9_\-]{20,}`, KindAdmin),
			// Legacy user keys carry "T3BlbkFJ" (base64 of "OpenAI") in the middle.
			p(`sk-[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20}`, KindAPI),
		}},
	{ID: "google", Name: "Google AI Studio", CheckURL: "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1", Header: "x-goog-api-key",
		KeysURL: "https://aistudio.google.com/apikey", CapURL: "https://console.cloud.google.com/billing",
		aliases:  []string{"gemini", "google"},
		patterns: []pattern{p(`AIza[0-9A-Za-z_\-]{35}`, KindAPI)}},
	{ID: "groq", Name: "Groq", CheckURL: "https://api.groq.com/openai/v1/models",
		KeysURL:  "https://console.groq.com/keys",
		aliases:  []string{"groq"},
		patterns: []pattern{p(`gsk_[A-Za-z0-9]{40,}`, KindAPI)}},
	{ID: "xai", Name: "xAI", CheckURL: "https://api.x.ai/v1/api-key",
		KeysURL:  "https://console.x.ai",
		aliases:  []string{"xai", "grok"},
		patterns: []pattern{p(`xai-[A-Za-z0-9]{40,}`, KindAPI)}},
	{ID: "huggingface", Name: "Hugging Face", CheckURL: "https://huggingface.co/api/whoami-v2",
		KeysURL:  "https://huggingface.co/settings/tokens",
		aliases:  []string{"huggingface", "hf_token"},
		patterns: []pattern{p(`hf_[A-Za-z0-9]{30,}`, KindAPI)}},
	{ID: "cerebras", Name: "Cerebras", CheckURL: "https://api.cerebras.ai/v1/models",
		KeysURL:  "https://cloud.cerebras.ai",
		aliases:  []string{"cerebras"},
		patterns: []pattern{p(`csk-[a-z0-9]{40,}`, KindAPI)}},
	{ID: "fireworks", Name: "Fireworks", CheckURL: "https://api.fireworks.ai/inference/v1/models",
		KeysURL:  "https://fireworks.ai/account/api-keys",
		aliases:  []string{"fireworks"},
		patterns: []pattern{p(`fw_[A-Za-z0-9]{20,}`, KindAPI)}},
	{ID: "perplexity", Name: "Perplexity", // no free read-only endpoint: map only
		KeysURL:  "https://www.perplexity.ai/account/api/keys",
		aliases:  []string{"perplexity", "pplx"},
		patterns: []pattern{p(`pplx-[A-Za-z0-9]{40,}`, KindAPI)}},
	{ID: "together", Name: "Together AI", CheckURL: "https://api.together.xyz/v1/models",
		KeysURL:   "https://api.together.ai/settings/api-keys",
		aliases:   []string{"together"},
		patterns:  []pattern{p(`tgp_v1_[A-Za-z0-9_\-]{30,}`, KindAPI)},
		ctxValues: regexp.MustCompile(`^[0-9a-f]{64}$`)},
	{ID: "deepseek", Name: "DeepSeek", CheckURL: "https://api.deepseek.com/user/balance",
		KeysURL:   "https://platform.deepseek.com/api_keys",
		aliases:   []string{"deepseek"},
		ctxValues: regexp.MustCompile(`^sk-[0-9a-f]{32}$`)},
	{ID: "moonshot", Name: "Moonshot (Kimi)", CheckURL: "https://api.moonshot.ai/v1/models",
		KeysURL:   "https://platform.moonshot.ai/console/api-keys",
		aliases:   []string{"moonshot", "kimi"},
		ctxValues: regexp.MustCompile(`^sk-[A-Za-z0-9]{40,}$`)},
	{ID: "mistral", Name: "Mistral", CheckURL: "https://api.mistral.ai/v1/models",
		KeysURL:   "https://console.mistral.ai/api-keys",
		aliases:   []string{"mistral", "codestral"},
		ctxValues: regexp.MustCompile(`^[A-Za-z0-9]{32}$`)},
	{ID: "zai", Name: "Z.ai (GLM)", CheckURL: "https://api.z.ai/api/paas/v4/models",
		KeysURL:   "https://z.ai/manage-apikey/apikey-list",
		aliases:   []string{"zai", "z_ai", "zhipu", "glm", "bigmodel"},
		ctxValues: regexp.MustCompile(`^[0-9a-f]{32}\.[A-Za-z0-9]{16}$`)},
}

// ByID returns the provider with that id, or nil.
func ByID(id string) *Provider {
	for _, pr := range All {
		if pr.ID == id {
			return pr
		}
	}
	if id == Unknown.ID {
		return Unknown
	}
	return nil
}

// Match is one key found in a piece of text.
type Match struct {
	Provider *Provider
	Kind     Kind
	Value    string
	Start    int // byte offset in the scanned text
}

var (
	// candidate values sitting in an assignment: NAME=value, "name": "value", name: value
	assignRe  = regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]*(?:key|token|secret)[A-Za-z0-9_.\-]*)["']?\s*[:=]\s*["']?([A-Za-z0-9_.\-]{24,})`)
	genericSK = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{32,}`)
)

// Find returns every provider key in text. Offsets are byte offsets.
func Find(text []byte) []Match {
	var out []Match
	taken := map[int]bool{}
	add := func(m Match) {
		if taken[m.Start] {
			return
		}
		taken[m.Start] = true
		out = append(out, m)
	}
	for _, pr := range All {
		for _, pt := range pr.patterns {
			for _, loc := range pt.re.FindAllIndex(text, -1) {
				if !boundary(text, loc[0], loc[1]) {
					continue
				}
				add(Match{Provider: pr, Kind: pt.kind, Value: string(text[loc[0]:loc[1]]), Start: loc[0]})
			}
		}
	}
	// Context-only shapes: accept a value only when the variable name or the
	// enclosing JSON object names the provider (DEEPSEEK_API_KEY=sk-…).
	for _, m := range assignRe.FindAllSubmatchIndex(text, -1) {
		val := string(text[m[4]:m[5]])
		if taken[m[4]] || !boundary(text, m[4], m[5]) {
			continue
		}
		ctx := strings.ToLower(string(text[m[2]:m[3]])) + " " + strings.ToLower(context(text, m[2]))
		for _, pr := range All {
			if pr.ctxValues == nil || !pr.ctxValues.MatchString(val) {
				continue
			}
			if mentions(ctx, pr.aliases) {
				add(Match{Provider: pr, Kind: KindAPI, Value: val, Start: m[4]})
				break
			}
		}
	}
	// Anything else that looks like sk-… is mapped with an unknown issuer.
	for _, loc := range genericSK.FindAllIndex(text, -1) {
		if taken[loc[0]] || !boundary(text, loc[0], loc[1]) || covered(out, loc[0]) {
			continue
		}
		add(Match{Provider: Unknown, Kind: KindAPI, Value: string(text[loc[0]:loc[1]]), Start: loc[0]})
	}
	return out
}

func covered(ms []Match, at int) bool {
	for _, m := range ms {
		if at >= m.Start && at < m.Start+len(m.Value) {
			return true
		}
	}
	return false
}

// boundary rejects matches glued to other key characters (part of a longer token).
func boundary(text []byte, s, e int) bool {
	isTok := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
	}
	if s > 0 && isTok(text[s-1]) {
		return false
	}
	if e < len(text) && isTok(text[e]) {
		return false
	}
	return true
}

// context returns up to 3 preceding lines (enough to see the JSON object key
// in OpenCode's auth.json or a TOML table header).
func context(text []byte, at int) string {
	start := at
	lines := 0
	for start > 0 && lines < 4 && at-start < 200 {
		start--
		if text[start] == '\n' {
			lines++
		}
	}
	return string(text[start:at])
}

func mentions(s string, aliases []string) bool {
	for _, a := range aliases {
		if strings.Contains(s, a) {
			return true
		}
	}
	return false
}

// Hint is the public part of a key: its issuer prefix and last 4 characters,
// the same hint provider dashboards show next to each key.
func Hint(value string) string {
	prefix := ""
	for _, pf := range []string{"sk-ant-api03-", "sk-ant-admin01-", "sk-ant-oat01-", "sk-ant-ort01-", "sk-or-v1-", "sk-proj-", "sk-svcacct-", "sk-admin-", "sk-", "AIza", "gsk_", "xai-", "hf_", "csk-", "fw_", "pplx-", "tgp_v1_"} {
		if strings.HasPrefix(value, pf) {
			prefix = pf
			break
		}
	}
	if strings.HasPrefix(prefix, "sk-ant-") {
		prefix = "sk-ant-"
	}
	if len(value) < 12 {
		return prefix + "…"
	}
	return prefix + "…" + value[len(value)-4:]
}

// Accepts reports whether value, on its own, has this provider's key shape.
func (p *Provider) Accepts(value string) (Kind, bool) {
	for _, pt := range p.patterns {
		if loc := pt.re.FindStringIndex(value); loc != nil && loc[0] == 0 && loc[1] == len(value) {
			return pt.kind, true
		}
	}
	if p.ctxValues != nil && p.ctxValues.MatchString(value) {
		return KindAPI, true
	}
	return "", false
}
