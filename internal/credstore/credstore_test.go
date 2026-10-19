package credstore

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Blackman99/keyroll/internal/testhome"
)

// fixture reads a synthetic command output. {{SECRET}} is replaced by a fake
// key at run time, standing in for anything secret-looking a store might print:
// it must never reach an Entry.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(b), "{{SECRET}}", testhome.Fake().Anthropic))
}

func noSecret(t *testing.T, r Result) {
	t.Helper()
	b, _ := json.Marshal(r)
	secret := testhome.Fake().Anthropic
	for i := 0; i+8 <= len(secret); i++ {
		if strings.Contains(string(b), secret[i:i+8]) {
			t.Fatalf("result carries part of a secret: %s", b)
		}
	}
}

func onlySafeCalls(t *testing.T, c *Canned) {
	t.Helper()
	for _, call := range c.Calls {
		f := strings.Fields(call)
		for _, a := range f[1:] {
			if a == "-w" || a == "-g" || a == "-d" || a == "dump-keychain" || a == "lookup" || a == "search" || strings.Contains(a, "GetSecret") {
				t.Errorf("unsafe call: %s", call)
			}
		}
		if f[0] == "secret-tool" {
			t.Errorf("secret-tool prints secrets, never use it: %s", call)
		}
	}
}

func TestMacOS(t *testing.T) {
	c := &Canned{Replies: map[string]Reply{
		"security find-generic-password -s Claude Code":                {fixture(t, "security-generic-claude.txt"), 0},
		"security find-generic-password -s Claude Code-credentials":    {fixture(t, "security-generic-claude-credentials.txt"), 0},
		"security find-generic-password -s goose":                      {fixture(t, "security-generic-goose.txt"), 0},
		"security find-internet-password -s https://api.openai.com/v1": {fixture(t, "security-internet-zed-openai.txt"), 0},
	}}
	r := Lookup(context.Background(), Env{GOOS: "darwin", Run: c.Run})
	if r.Skipped != "" || r.Store != "macOS Keychain" || r.Asked != 13 {
		t.Fatalf("%+v", r)
	}
	if len(r.Entries) != 4 {
		t.Fatalf("entries: %+v", r.Entries)
	}
	want := []struct {
		tool, name, acct, prov, mod string
		holds                       Holds
	}{
		{"Claude Code", "Claude Code", "sam", "anthropic", "2026-10-03", APIKey},
		{"Claude Code", "Claude Code-credentials", "sam", "anthropic", "2026-10-18", LoginTokens},
		{"Goose", "goose", "secrets", "", "2026-09-22", APIKeys},
		{"Zed", "https://api.openai.com/v1", "Bearer", "openai", "2026-05-15", APIKey},
	}
	for i, w := range want {
		e := r.Entries[i]
		if e.Tool != w.tool || e.Name != w.name || e.Account != w.acct || e.Provider != w.prov || e.Modified != w.mod || e.Holds != w.holds || e.Manual == "" {
			t.Errorf("entry %d = %+v, want %+v", i, e, w)
		}
	}
	if r.Entries[0].Command != "security find-generic-password -s 'Claude Code'" {
		t.Errorf("command = %q", r.Entries[0].Command)
	}
	if got := r.For("anthropic"); len(got) != 2 || got[0].Name != "Claude Code" || got[1].Tool != "Goose" {
		t.Errorf("For(anthropic) = %+v (the login entry must be left out, Goose kept)", got)
	}
	if got := r.For("openai"); len(got) != 2 || got[1].Tool != "Zed" {
		t.Errorf("For(openai) = %+v", got)
	}
	onlySafeCalls(t, c)
	noSecret(t, r)
}

func TestMacOSHashedClaudeConfigDir(t *testing.T) {
	c := &Canned{}
	env := func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return "/Users/sam/.claude-work"
		}
		return ""
	}
	r := Lookup(context.Background(), Env{GOOS: "darwin", Getenv: env, Run: c.Run})
	if r.Asked != 15 || len(r.Entries) != 0 || r.Skipped != "" {
		t.Fatalf("%+v", r)
	}
	// sha256("/Users/sam/.claude-work")[:8], the suffix Claude Code appends
	joined := strings.Join(c.Calls, "\n")
	for _, want := range []string{"-s Claude Code-credentials-", "-s Claude Code-"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if got := claudeService("-credentials", "/Users/sam/.claude-work"); len(got) != len("Claude Code-credentials-")+8 {
		t.Errorf("hashed service %q", got)
	}
}

func TestMacOSLockedKeychain(t *testing.T) {
	c := &Canned{Missing: context.DeadlineExceeded}
	r := Lookup(context.Background(), Env{GOOS: "darwin", Run: c.Run})
	if !strings.Contains(r.Skipped, "did not answer") || len(r.Entries) != 0 {
		t.Fatalf("%+v calls=%d", r, len(c.Calls))
	}
}

func TestParseSecurityIgnoresEverythingButPublicAttributes(t *testing.T) {
	a := ParseSecurity(fixture(t, "security-generic-claude-credentials.txt"))
	if a.Service != "Claude Code-credentials" || a.Account != "sam" || a.Modified != "2026-10-18" || !strings.HasSuffix(a.Keychain, "login.keychain-db") {
		t.Fatalf("%+v", a)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "FAKE") {
		t.Fatalf("password line leaked into attributes: %s", b)
	}
}

func TestWindows(t *testing.T) {
	c := &Canned{Replies: map[string]Reply{"cmdkey /list": {fixture(t, "cmdkey-list-en.txt"), 0}}}
	r := Lookup(context.Background(), Env{GOOS: "windows", Run: c.Run})
	if r.Skipped != "" || r.Store != "Windows Credential Manager" {
		t.Fatalf("%+v", r)
	}
	got := map[string]Entry{}
	for _, e := range r.Entries {
		got[e.Name] = e
	}
	if len(r.Entries) != 5 {
		t.Fatalf("want 5 AI entries (goose, codex, gemini, 2× zed), got %+v", r.Entries)
	}
	if e := got["secrets.goose"]; e.Tool != "Goose" || e.Holds != APIKeys {
		t.Errorf("goose: %+v", e)
	}
	if e := got["cli|5f3a9c0e1b2d4a68.Codex Auth"]; e.Tool != "Codex" || e.Provider != "openai" || e.Account != "cli|5f3a9c0e1b2d4a68" {
		t.Errorf("codex: %+v", e)
	}
	if e := got["gemini-cli-api-key/default-api-key"]; e.Tool != "Gemini CLI" || e.Provider != "google" || e.Holds != APIKey {
		t.Errorf("gemini: %+v", e)
	}
	if e := got["zed:url=https://api.anthropic.com"]; e.Tool != "Zed" || e.Provider != "anthropic" {
		t.Errorf("zed anthropic: %+v", e)
	}
	if e := got["zed:url=https://llm.internal.example/v1"]; e.Tool != "Zed" || e.Provider != "" || !strings.Contains(e.Manual, "this API URL") {
		t.Errorf("zed custom url: %+v", e)
	}
	onlySafeCalls(t, c)
	if len(c.Calls) != 1 || c.Calls[0] != "cmdkey /list" {
		t.Errorf("calls: %v", c.Calls)
	}
}

func TestWindowsLocalisedAndEmpty(t *testing.T) {
	c := &Canned{Replies: map[string]Reply{"cmdkey /list": {fixture(t, "cmdkey-list-de.txt"), 0}}}
	r := Lookup(context.Background(), Env{GOOS: "windows", Run: c.Run})
	if len(r.Entries) != 1 || r.Entries[0].Holds != LoginTokens || r.Entries[0].Account != "main-account" {
		t.Fatalf("german cmdkey: %+v", r)
	}
	// UTF-16 output is tolerated
	u16 := []byte{}
	for _, b := range fixture(t, "cmdkey-list-de.txt") {
		u16 = append(u16, b, 0)
	}
	if ts := ParseCmdkey(u16); len(ts) != 2 || ts[0] != "gemini-cli-oauth/main-account" {
		t.Fatalf("utf-16: %q", ts)
	}
	c = &Canned{Replies: map[string]Reply{"cmdkey /list": {fixture(t, "cmdkey-list-empty.txt"), 0}}}
	if r := Lookup(context.Background(), Env{GOOS: "windows", Run: c.Run}); len(r.Entries) != 0 || r.Skipped != "" {
		t.Fatalf("empty: %+v", r)
	}
}

func ssEnv(k string) string {
	if k == "DBUS_SESSION_BUS_ADDRESS" {
		return "unix:path=/run/user/1000/bus"
	}
	return ""
}

func TestSecretService(t *testing.T) {
	c := &Canned{Replies: map[string]Reply{
		"gdbus {'service': 'goose', 'username': 'secrets'}": {fixture(t, "gdbus-unlocked.txt"), 0},
		"gdbus {'url': 'https://api.deepseek.com/v1'}":      {fixture(t, "gdbus-locked.txt"), 0},
		"gdbus {'service': 'Codex Auth'}":                   {fixture(t, "gdbus-empty.txt"), 0},
	}}
	r := Lookup(context.Background(), Env{GOOS: "linux", Getenv: ssEnv, Run: c.Run})
	if r.Skipped != "" || r.Store != "Secret Service" || r.Asked != 11 || len(r.Entries) != 2 {
		t.Fatalf("%+v", r)
	}
	g, z := r.Entries[0], r.Entries[1]
	if g.Tool != "Goose" || g.Name != "service=goose username=secrets" || g.Locked || g.Account != "secrets" {
		t.Errorf("goose: %+v", g)
	}
	if z.Tool != "Zed" || z.Provider != "deepseek" || !z.Locked {
		t.Errorf("zed: %+v", z)
	}
	if !strings.Contains(g.Command, "Secret.Service.SearchItems") {
		t.Errorf("command: %s", g.Command)
	}
	onlySafeCalls(t, c)
	for _, call := range c.Calls {
		if !strings.HasPrefix(call, "gdbus call --session") || !strings.Contains(call, "SearchItems") {
			t.Errorf("unexpected call %s", call)
		}
	}
}

func TestSecretServiceSkips(t *testing.T) {
	r := Lookup(context.Background(), Env{GOOS: "linux", Run: (&Canned{}).Run})
	if !strings.Contains(r.Skipped, "no D-Bus session") {
		t.Errorf("headless: %+v", r)
	}
	r = Lookup(context.Background(), Env{GOOS: "linux", Getenv: ssEnv, Run: (&Canned{Missing: exec.ErrNotFound}).Run})
	if !strings.Contains(r.Skipped, "gdbus") {
		t.Errorf("no gdbus: %+v", r)
	}
	c := &Canned{Replies: map[string]Reply{"gdbus {'service': 'Codex Auth'}": {nil, 1}}}
	r = Lookup(context.Background(), Env{GOOS: "linux", Getenv: ssEnv, Run: c.Run})
	if !strings.Contains(r.Skipped, "no Secret Service") || len(c.Calls) != 1 {
		t.Errorf("no service: %+v (%d calls)", r, len(c.Calls))
	}
}

func TestParseSearchItems(t *testing.T) {
	for _, tc := range []struct {
		in     string
		un, lo int
		ok     bool
	}{
		{"([objectpath '/org/freedesktop/secrets/collection/login/1'], @ao [])", 1, 0, true},
		{"(@ao [], [objectpath '/org/freedesktop/secrets/collection/kdewallet/3', objectpath '/org/freedesktop/secrets/collection/kdewallet/4'])", 0, 2, true},
		{"(@ao [], @ao [])", 0, 0, true},
		{"Error: GDBus.Error:org.freedesktop.DBus.Error.ServiceUnknown", 0, 0, false},
	} {
		un, lo, ok := ParseSearchItems([]byte(tc.in))
		if un != tc.un || lo != tc.lo || ok != tc.ok {
			t.Errorf("%q: %d %d %v", tc.in, un, lo, ok)
		}
	}
}

func TestUnsafeCommandsAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"security", "find-generic-password", "-s", "goose", "-w"},
		{"security", "find-generic-password", "-g", "-s", "goose"},
		{"security", "dump-keychain", "-d"},
		{"secret-tool", "lookup", "service", "goose"},
		{"secret-tool", "search", "service", "goose"},
		{"gdbus", "call", "--method", "org.freedesktop.Secret.Service.GetSecrets"},
		{"cmdkey", "/generic:x", "/pass:y"},
		{"sh", "-c", "security find-generic-password -w -s goose"},
	} {
		if err := safe(args[0], args[1:]); err == nil {
			t.Errorf("allowed: %v", args)
		}
		if _, _, err := Exec(context.Background(), args[0], args[1:]...); err == nil {
			t.Errorf("Exec ran: %v", args)
		}
	}
}

func TestFilter(t *testing.T) {
	r := Result{Entries: []Entry{{Tool: "Zed", Provider: "openai"}, {Tool: "Goose"}, {Tool: "Claude Code", Provider: "anthropic"}}}
	if f := r.Filter("anthropic"); len(f.Entries) != 2 || f.Entries[0].Tool != "Goose" {
		t.Errorf("%+v", f)
	}
	if f := r.Filter(""); len(f.Entries) != 3 {
		t.Errorf("%+v", f)
	}
}
