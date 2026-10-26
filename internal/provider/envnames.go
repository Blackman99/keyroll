package provider

import "strings"

// envNames are the environment variable names each issuer's own SDK, CLI or
// the AI tools keyroll knows read the key from. A GitHub Actions secret with
// one of these names almost certainly holds that issuer's key: workflows pass
// secrets to tools as env vars, under the name the tool expects.
var envNames = map[string][]nameKind{
	"anthropic": {{"ANTHROPIC_API_KEY", KindAPI}, {"CLAUDE_API_KEY", KindAPI},
		{"ANTHROPIC_ADMIN_KEY", KindAdmin}, {"ANTHROPIC_ADMIN_API_KEY", KindAdmin},
		// anthropics/claude-code-action: the claude.ai login `claude setup-token` prints
		{"CLAUDE_CODE_OAUTH_TOKEN", KindLogin}},
	"openai":      {{"OPENAI_API_KEY", KindAPI}, {"CODEX_API_KEY", KindAPI}, {"OPENAI_ADMIN_KEY", KindAdmin}},
	"openrouter":  {{"OPENROUTER_API_KEY", KindAPI}},
	"google":      {{"GEMINI_API_KEY", KindAPI}, {"GOOGLE_GENERATIVE_AI_API_KEY", KindAPI}, {"GOOGLE_API_KEY", KindAPI}},
	"groq":        {{"GROQ_API_KEY", KindAPI}},
	"xai":         {{"XAI_API_KEY", KindAPI}, {"GROK_API_KEY", KindAPI}},
	"huggingface": {{"HF_TOKEN", KindAPI}, {"HUGGING_FACE_HUB_TOKEN", KindAPI}, {"HUGGINGFACEHUB_API_TOKEN", KindAPI}, {"HF_API_TOKEN", KindAPI}},
	"cerebras":    {{"CEREBRAS_API_KEY", KindAPI}},
	"fireworks":   {{"FIREWORKS_API_KEY", KindAPI}},
	"perplexity":  {{"PERPLEXITY_API_KEY", KindAPI}, {"PPLX_API_KEY", KindAPI}},
	"together":    {{"TOGETHER_API_KEY", KindAPI}, {"TOGETHER_AI_API_KEY", KindAPI}},
	"deepseek":    {{"DEEPSEEK_API_KEY", KindAPI}},
	"moonshot":    {{"MOONSHOT_API_KEY", KindAPI}, {"KIMI_API_KEY", KindAPI}},
	"mistral":     {{"MISTRAL_API_KEY", KindAPI}, {"CODESTRAL_API_KEY", KindAPI}},
	"zai":         {{"ZAI_API_KEY", KindAPI}, {"Z_AI_API_KEY", KindAPI}, {"ZHIPUAI_API_KEY", KindAPI}, {"GLM_API_KEY", KindAPI}},
}

type nameKind struct {
	name string
	kind Kind
}

// nameWords are the words in a variable name that point at an issuer.
// Words of 5+ letters may sit inside a longer word (OPENAIKEY); shorter ones
// must be a whole _-separated word (HF_HUB_TOKEN, not SHELF_TOKEN).
var nameWords = []struct{ word, id string }{
	{"OPENROUTER", "openrouter"}, {"ANTHROPIC", "anthropic"}, {"CLAUDE", "anthropic"},
	{"OPENAI", "openai"}, {"CODEX", "openai"}, {"GEMINI", "google"},
	{"GROQ", "groq"}, {"XAI", "xai"}, {"GROK", "xai"},
	{"HUGGINGFACE", "huggingface"}, {"HUGGING", "huggingface"}, {"HF", "huggingface"},
	{"CEREBRAS", "cerebras"}, {"FIREWORKS", "fireworks"},
	{"PERPLEXITY", "perplexity"}, {"PPLX", "perplexity"}, {"TOGETHER", "together"},
	{"DEEPSEEK", "deepseek"}, {"MOONSHOT", "moonshot"}, {"KIMI", "moonshot"},
	{"MISTRAL", "mistral"}, {"CODESTRAL", "mistral"},
	{"ZHIPUAI", "zai"}, {"ZHIPU", "zai"}, {"ZAI", "zai"}, {"GLM", "zai"}, {"BIGMODEL", "zai"},
}

// EnvNames returns the variable names this issuer's tools read its API key from.
func (p *Provider) EnvNames() []string {
	var out []string
	for _, nk := range envNames[p.ID] {
		if nk.kind == KindAPI {
			out = append(out, nk.name)
		}
	}
	return out
}

// NameMatch says which issuer a variable or secret name points at.
type NameMatch struct {
	Provider *Provider
	Kind     Kind
	Known    bool   // the exact name an issuer's SDK or a known tool reads
	Word     string // otherwise: the word in the name that names the issuer
}

// ForName tells, from a name alone (OPENAI_API_KEY, PROD_ANTHROPIC_KEY),
// whether it looks like it holds an AI provider credential. ok is false for
// names that do not mention an issuer or do not look like a credential.
func ForName(name string) (m NameMatch, ok bool) {
	n := strings.ToUpper(strings.TrimSpace(name))
	for _, pr := range All {
		for _, nk := range envNames[pr.ID] {
			if nk.name == n {
				return NameMatch{Provider: pr, Kind: nk.kind, Known: true}, true
			}
		}
	}
	words := strings.FieldsFunc(n, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	has := func(w string) bool {
		for _, x := range words {
			if x == w || len(w) >= 5 && strings.Contains(x, w) {
				return true
			}
		}
		return false
	}
	if !strings.Contains(n, "KEY") && !strings.Contains(n, "TOKEN") && !strings.Contains(n, "SECRET") {
		return NameMatch{}, false
	}
	for _, nw := range nameWords {
		if !has(nw.word) {
			continue
		}
		kind := KindAPI
		switch {
		case has("OAUTH") || has("SESSION") || has("REFRESH"):
			kind = KindLogin
		case has("ADMIN"):
			kind = KindAdmin
		}
		return NameMatch{Provider: ByID(nw.id), Kind: kind, Word: nw.word}, true
	}
	return NameMatch{}, false
}
