package provider

import (
	"strings"
	"testing"

	"github.com/Blackman99/keyroll/internal/testhome"
)

func TestFindEachProvider(t *testing.T) {
	k := testhome.Fake()
	cases := map[string]string{
		"anthropic":  "export ANTHROPIC_API_KEY=" + k.Anthropic,
		"openrouter": `"key": "` + k.OpenRouter + `"`,
		"openai":     "OPENAI_API_KEY=" + k.OpenAI,
		"google":     "GEMINI_API_KEY=" + k.Google,
		"deepseek":   "DEEPSEEK_API_KEY=" + k.DeepSeek,
	}
	for want, text := range cases {
		ms := Find([]byte(text))
		if len(ms) != 1 {
			t.Fatalf("%s: got %d matches", want, len(ms))
		}
		if ms[0].Provider.ID != want {
			t.Errorf("%s: got provider %s", want, ms[0].Provider.ID)
		}
	}
}

func TestContextOnlyNeedsContext(t *testing.T) {
	k := testhome.Fake()
	// A bare sk-<hex32> with no DeepSeek context is mapped as unknown, never guessed.
	ms := Find([]byte("SOME_TOKEN=" + k.DeepSeek))
	if len(ms) != 1 || ms[0].Provider.ID != "unknown" {
		t.Fatalf("got %+v", ms)
	}
	// OpenCode auth.json style: the provider is the enclosing object key.
	js := "{\n  \"deepseek\": {\n    \"type\": \"api\",\n    \"key\": \"" + k.DeepSeek + "\"\n  }\n}"
	ms = Find([]byte(js))
	if len(ms) != 1 || ms[0].Provider.ID != "deepseek" {
		t.Fatalf("got %+v", ms)
	}
}

func TestBoundary(t *testing.T) {
	k := testhome.Fake()
	if ms := Find([]byte("x" + k.Google + "y")); len(ms) != 0 {
		t.Fatalf("matched inside a longer token: %+v", ms)
	}
}

func TestHintAndAccepts(t *testing.T) {
	k := testhome.Fake()
	h := Hint(k.Anthropic)
	if !strings.HasPrefix(h, "sk-ant-…") || len([]rune(h)) != len("sk-ant-")+1+4 {
		t.Fatalf("hint %q", h)
	}
	an := ByID("anthropic")
	if _, ok := an.Accepts(k.NewAnthropic); !ok {
		t.Fatal("anthropic should accept its own key")
	}
	if _, ok := an.Accepts(k.OpenAI); ok {
		t.Fatal("anthropic accepted an OpenAI key")
	}
	if kind, _ := an.Accepts("sk-ant-" + "oat01-" + strings.Repeat("a", 40)); kind != KindLogin {
		t.Fatal("oat token should be a login token")
	}
}

func TestForName(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		kind     Kind
		known    bool
	}{
		{"OPENAI_API_KEY", "openai", KindAPI, true},
		{"anthropic_api_key", "anthropic", KindAPI, true},
		{"CLAUDE_CODE_OAUTH_TOKEN", "anthropic", KindLogin, true},
		{"OPENAI_ADMIN_KEY", "openai", KindAdmin, true},
		{"HF_TOKEN", "huggingface", KindAPI, true},
		{"GEMINI_API_KEY", "google", KindAPI, true},
		{"PROD_ANTHROPIC_KEY", "anthropic", KindAPI, false},
		{"OPENROUTERKEY", "openrouter", KindAPI, false},
		{"STAGING_OPENAI_ADMIN_TOKEN", "openai", KindAdmin, false},
		{"HF_HUB_TOKEN", "huggingface", KindAPI, false},
		{"MY_CLAUDE_OAUTH_TOKEN", "anthropic", KindLogin, false},
		{"DEEPSEEK_SECRET", "deepseek", KindAPI, false},
	} {
		m, ok := ForName(tc.name)
		if !ok || m.Provider.ID != tc.id || m.Kind != tc.kind || m.Known != tc.known {
			t.Errorf("%s: got %+v ok=%v", tc.name, m, ok)
		}
	}
	for _, no := range []string{"NPM_TOKEN", "OPENAI_BASE_URL", "SHELF_TOKEN", "GOOGLE_MAPS_API_KEY", "DOCKERHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "XAIVER_KEY"} {
		if m, ok := ForName(no); ok {
			t.Errorf("%s should not match, got %s", no, m.Provider.ID)
		}
	}
}

func TestEveryProviderHasEnvNames(t *testing.T) {
	for _, p := range All {
		if len(p.EnvNames()) == 0 {
			t.Errorf("%s has no env var names", p.ID)
		}
	}
}
