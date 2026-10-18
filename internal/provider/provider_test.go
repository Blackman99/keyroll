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
