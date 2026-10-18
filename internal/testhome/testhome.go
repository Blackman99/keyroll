// Package testhome builds a fake home directory with obviously fake provider
// keys in the places real tools put them. Keys are assembled at run time so no
// key-shaped literal ever sits in this repository.
package testhome

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Keys used by the fake home.
type Keys struct {
	Anthropic, OpenRouter, OpenAI, Google, DeepSeek, NewAnthropic, NewOpenAI string
}

const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func gen(seed string, n int, alphabet string) string {
	var b strings.Builder
	i := 0
	for b.Len() < n {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", seed, i)))
		for _, c := range sum {
			if b.Len() == n {
				break
			}
			b.WriteByte(alphabet[int(c)%len(alphabet)])
		}
		i++
	}
	return b.String()
}

func hexs(seed string, n int) string {
	var s string
	for i := 0; len(s) < n; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", seed, i)))
		s += hex.EncodeToString(sum[:])
	}
	return s[:n]
}

// Fake returns deterministic fake keys. Each contains "FAKE" / "fake" so a
// human reading a file can tell at a glance.
func Fake() Keys {
	return Keys{
		Anthropic:    "sk-ant-" + "api03-" + "FAKEkeyroll" + gen("anthropic", 82, alnum+"-_") + "AA",
		NewAnthropic: "sk-ant-" + "api03-" + "FAKEkeyroll" + gen("anthropic-new", 82, alnum+"-_") + "AA",
		OpenRouter:   "sk-or-" + "v1-" + "fa4e" + hexs("openrouter", 60),
		OpenAI:       "sk-" + "proj-" + "FAKEkeyroll" + gen("openai", 120, alnum+"-_"),
		NewOpenAI:    "sk-" + "proj-" + "FAKEkeyroll" + gen("openai-new", 120, alnum+"-_"),
		Google:       "AI" + "za" + "SyFAKEkeyroll" + gen("google", 22, alnum+"-_"),
		DeepSeek:     "sk-" + "fa4e" + hexs("deepseek", 28),
	}
}

func write(home, rel string, mode os.FileMode, body string) {
	p := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		panic(err)
	}
}

// Build writes the fake home: one Anthropic key pasted into 9 places, the
// scenario @HeyAIpenguin described after the OpenCode Console breach
// ("1 API token, 7 copies; Claude Code's auto-backups cloned it for me").
func Build(home string) Keys {
	k := Fake()
	claudeJSON := `{
  "numStartups": 212,
  "installMethod": "native",
  "primaryApiKey": "` + k.Anthropic + `",
  "hasCompletedOnboarding": true,
  "projects": {}
}
`
	write(home, ".claude.json", 0o600, claudeJSON)
	write(home, ".claude/backups/.claude.json.backup.1760650211", 0o600, claudeJSON)
	write(home, ".claude/backups/.claude.json.backup.1760736622", 0o600, claudeJSON)
	write(home, ".claude/settings.json", 0o644, `{
  "permissions": {"defaultMode": "acceptEdits"},
  "env": {"DISABLE_TELEMETRY": "1"}
}
`)
	// OpenCode v2: credentials in SQLite, legacy auth.json left behind.
	write(home, ".local/share/opencode/auth.json", 0o600, `{
  "anthropic": {
    "type": "api",
    "key": "`+k.Anthropic+`"
  },
  "openrouter": {
    "type": "api",
    "key": "`+k.OpenRouter+`"
  }
}
`)
	write(home, ".local/share/opencode/opencode.db", 0o644, "SQLite format 3\x00\x10\x00\x01\x01\x00@  \x00\x00\x00"+
		strings.Repeat("\x00", 80)+`{"type":"key","key":"`+k.Anthropic+`"}`+strings.Repeat("\x00", 40)+
		`{"type":"key","key":"`+k.OpenRouter+`"}`+strings.Repeat("\x00", 200))
	write(home, ".config/opencode/opencode.json", 0o644, `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "deepseek": {
      "options": {
        "apiKey": "`+k.DeepSeek+`"
      }
    }
  }
}
`)
	write(home, ".codex/auth.json", 0o600, `{
  "auth_mode": "apikey",
  "OPENAI_API_KEY": "`+k.OpenAI+`"
}
`)
	write(home, ".codex/config.toml", 0o644, "model = \"gpt-5.5-codex\"\napproval_policy = \"on-request\"\n")
	write(home, ".config/io.datasette.llm/keys.json", 0o600, `{
  "// Note": "This file stores secret API credentials. Do not share!",
  "openai": "`+k.OpenAI+`"
}
`)
	write(home, ".cline/data/secrets.json", 0o600, `{
  "openRouterApiKey": "`+k.OpenRouter+`"
}
`)
	write(home, ".gemini/.env", 0o600, "GEMINI_API_KEY="+k.Google+"\n")
	write(home, ".zshrc", 0o644, `# ~/.zshrc
export PATH="$HOME/.local/bin:$PATH"
alias gs="git status"

# AI
export ANTHROPIC_API_KEY=`+k.Anthropic+`
export OPENROUTER_API_KEY="`+k.OpenRouter+`"
`)
	write(home, ".zsh_history", 0o600, ": 1760001000:0;git status\n: 1760001020:0;export ANTHROPIC_API_KEY="+k.Anthropic+"\n: 1760001090:0;claude\n: 1760002000:0;npm test\n")
	write(home, "code/shop/.env", 0o644, "DATABASE_URL=postgres://localhost/shop\nANTHROPIC_API_KEY="+k.Anthropic+"\nOPENAI_API_KEY="+k.OpenAI+"\n")
	write(home, "code/shop/.env.example", 0o644, "DATABASE_URL=\nANTHROPIC_API_KEY="+k.Anthropic+"\nOPENAI_API_KEY=\n")
	write(home, "code/shop/package.json", 0o644, `{"name":"shop","private":true}`+"\n")
	write(home, "code/agent-bot/.dev.vars", 0o644, "OPENROUTER_API_KEY="+k.OpenRouter+"\n")
	return k
}
