package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Blackman99/keyroll/internal/check"
	"github.com/Blackman99/keyroll/internal/ci"
	"github.com/Blackman99/keyroll/internal/credstore"
	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/testhome"
)

type run struct {
	code           int
	stdout, stderr string
}

func newApp(home string, stdin string, srv *httptest.Server) (*app, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	c := check.New("test")
	if srv != nil {
		c.Override = map[string]string{}
		for _, id := range []string{"anthropic", "openai", "openrouter", "google", "deepseek"} {
			c.Override[id] = srv.URL + "/" + id
		}
	}
	a := &app{stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin),
		env:     locate.Env{Home: home, GOOS: "linux", Getenv: func(string) string { return "" }},
		checker: c, now: func() time.Time { return time.Unix(1760750000, 0) },
		tty: func() (io.ReadWriteCloser, error) { return nil, errors.New("no tty in tests") },
		// tests never reach the real gh: as if it were not installed, unless a test sets one
		gh: (&ci.Canned{Missing: true}).Run}
	return a, &out, &errb
}

func kr(t *testing.T, home, stdin string, srv *httptest.Server, args ...string) run {
	t.Helper()
	a, out, errb := newApp(home, stdin, srv)
	code := a.run(args)
	return run{code, out.String(), errb.String()}
}

// fakeProviders: OpenRouter says alive with no cap, everyone else says 401.
func fakeProviders() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/openrouter" {
			w.Write([]byte(`{"data":{"limit":null,"usage":412.2}}`))
			return
		}
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
}

// No output, in any mode, may contain any 8-character run of a key's secret part.
func assertNoLeak(t *testing.T, k testhome.Keys, outputs ...string) {
	t.Helper()
	all := strings.Join(outputs, "\n")
	for _, v := range []string{k.Anthropic, k.NewAnthropic, k.OpenRouter, k.OpenAI, k.Google, k.DeepSeek} {
		secret := v[len(v)-30:]
		for i := 0; i+8 <= len(secret)-4; i++ { // the last 4 characters are the public hint
			if strings.Contains(all, secret[i:i+8]) {
				t.Fatalf("output leaks part of a key: %q", secret[i:i+8])
			}
		}
	}
}

func TestMapTextAndJSON(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	r := kr(t, home, "", nil)
	if r.code != 0 || !strings.Contains(r.stdout, "9 copies") || !strings.Contains(r.stdout, "keyroll rotate ") {
		t.Fatalf("map: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "spillscan") {
		t.Fatal("map must point transcripts to spillscan")
	}
	j := kr(t, home, "", nil, "--json")
	var rep struct {
		Keys []struct {
			Fingerprint string `json:"fingerprint"`
			Copies      []any  `json:"copies"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(j.stdout), &rep); err != nil || len(rep.Keys) != 5 {
		t.Fatalf("json: %v %d", err, len(rep.Keys))
	}
	assertNoLeak(t, k, r.stdout, r.stderr, j.stdout)
}

func TestCheckFlagsDeadKeysStillConfigured(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	srv := fakeProviders()
	defer srv.Close()
	r := kr(t, home, "", srv, "check")
	if r.code != exitAction {
		t.Fatalf("exit %d, want %d\n%s", r.code, exitAction, r.stdout)
	}
	for _, want := range []string{"REJECTED", "ALIVE", "no spend limit", "their next request fails with HTTP 401", "not a bug in your code"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in\n%s", want, r.stdout)
		}
	}
	j := kr(t, home, "", srv, "check", "--json")
	assertNoLeak(t, k, r.stdout, r.stderr, j.stdout, j.stderr)
}

func TestRotateEndToEnd(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	m := kr(t, home, "", nil, "--json", "--provider", "anthropic")
	var rep struct {
		Keys []struct{ Fingerprint string } `json:"keys"`
	}
	json.Unmarshal([]byte(m.stdout), &rep)
	fp := rep.Keys[0].Fingerprint

	dry := kr(t, home, "", nil, "rotate", fp, "--dry-run")
	if dry.code != 0 || !strings.Contains(dry.stdout, "nothing changed") {
		t.Fatalf("dry run: %s %s", dry.stdout, dry.stderr)
	}
	// piped key without --yes and no terminal: refuse rather than guess
	noYes := kr(t, home, k.NewAnthropic+"\n", nil, "rotate", fp, "--offline")
	if noYes.code != exitUsage || !strings.Contains(noYes.stderr, "--yes") {
		t.Fatalf("expected a --yes hint: %d %s", noYes.code, noYes.stderr)
	}
	wrong := kr(t, home, k.OpenAI, nil, "rotate", fp, "--offline", "--yes")
	if wrong.code != exitUsage || !strings.Contains(wrong.stderr, "does not look like a Anthropic key") && !strings.Contains(wrong.stderr, "Anthropic") {
		t.Fatalf("wrong provider accepted: %s", wrong.stderr)
	}
	ok := kr(t, home, k.NewAnthropic+"\n", nil, "rotate", fp, "--offline", "--yes")
	if ok.code != 0 || !strings.Contains(ok.stdout, "now 1 (OpenCode database") || !strings.Contains(ok.stdout, "Treat it as NOT revoked") {
		t.Fatalf("rotate: %d\n%s\n%s", ok.code, ok.stdout, ok.stderr)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	if !strings.Contains(string(b), k.NewAnthropic) {
		t.Fatal("zshrc not rotated")
	}
	assertNoLeak(t, k, dry.stdout, noYes.stdout, noYes.stderr, wrong.stdout, wrong.stderr, ok.stdout, ok.stderr)
}

func TestRotateRefusesDeadNewKey(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	srv := fakeProviders()
	defer srv.Close()
	r := kr(t, home, k.NewAnthropic, srv, "rotate", "38bb", "--yes")
	if r.code != exitUsage || !strings.Contains(r.stderr, "the new key does not work") {
		t.Fatalf("%d %s", r.code, r.stderr)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if strings.Contains(string(b), k.NewAnthropic) {
		t.Fatal("a dead new key was written")
	}
}

func TestUsageErrors(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--jsn"}, "did you mean --json"},
		{[]string{"chek"}, "did you mean `keyroll check`"},
		{[]string{"--provider", "antropic"}, "unknown provider"},
		{[]string{"--root", "/definitely/not/here"}, "does not exist"},
		{[]string{"rotate"}, "exactly one fingerprint"},
		{[]string{"rotate", "ffffffff"}, "no key matches"},
		{[]string{"map", "dist"}, "--root dist"},
	} {
		r := kr(t, home, "", nil, tc.args...)
		if r.code != exitUsage || !strings.Contains(r.stderr, tc.want) || r.stdout != "" {
			t.Errorf("%v: code=%d stderr=%q stdout=%q", tc.args, r.code, r.stderr, r.stdout)
		}
	}
}

func TestVersionAndHelpFlags(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{{"version"}, {"--version"}} {
		r := kr(t, home, "", nil, args...)
		if r.code != exitOK || !strings.HasPrefix(r.stdout, "keyroll ") || r.stderr != "" {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, r.code, r.stdout, r.stderr)
		}
	}
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		r := kr(t, home, "", nil, args...)
		if r.code != exitOK || !strings.Contains(r.stdout, "usage") || r.stderr != "" {
			t.Errorf("%v: code=%d stderr=%q", args, r.code, r.stderr)
		}
	}
}

func TestEmptyHome(t *testing.T) {
	r := kr(t, t.TempDir(), "", nil)
	if r.code != 0 || !strings.Contains(r.stdout, "no AI provider keys found") {
		t.Fatalf("%d %s", r.code, r.stdout)
	}
}

func TestMCPNeverReturnsValues(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"key_map","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"key_copies","arguments":{"fingerprint":"38bb"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"nope"}`,
	}, "\n") + "\n"
	r := kr(t, home, in, nil, "mcp")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 responses (notification gets none), got %d:\n%s", len(lines), r.stdout)
	}
	if !strings.Contains(lines[1], `"readOnlyHint":true`) || !strings.Contains(lines[3], "primaryApiKey") || !strings.Contains(lines[4], "-32601") {
		t.Fatalf("unexpected MCP output:\n%s", r.stdout)
	}
	assertNoLeak(t, k, r.stdout)
}

// providers that accept exactly the keys in alive (by header value)
func acceptOnly(alive ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, k := range alive {
			if r.Header.Get("x-api-key") == k {
				w.Write([]byte(`{"data":[]}`))
				return
			}
		}
		w.WriteHeader(401)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error"}}`))
	}))
}

func TestRotateEndsWithOldKeyVerdict(t *testing.T) {
	// old key still accepted: the user must be told it is NOT revoked
	home := t.TempDir()
	k := testhome.Build(home)
	srv := acceptOnly(k.Anthropic, k.NewAnthropic)
	r := kr(t, home, k.NewAnthropic, srv, "rotate", "38bb", "--yes")
	srv.Close()
	if r.code != 0 || !strings.Contains(r.stdout, "still ACCEPTS it") || !strings.Contains(r.stdout, "NOT revoked") || !strings.Contains(r.stdout, "revoke the old key") {
		t.Fatalf("alive old key: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	assertNoLeak(t, k, r.stdout, r.stderr)

	// old key already refused: say so, and drop the revoke step
	home = t.TempDir()
	k = testhome.Build(home)
	srv = acceptOnly(k.NewAnthropic)
	defer srv.Close()
	r = kr(t, home, k.NewAnthropic, srv, "rotate", "38bb", "--yes")
	if r.code != 0 || !strings.Contains(r.stdout, "now REFUSES it") || strings.Contains(r.stdout, "revoke the old key") {
		t.Fatalf("revoked old key: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	j := kr(t, t.TempDir(), "", srv, "check", "--json")
	if j.stdout != "[]\n" {
		t.Fatalf("empty check json: %q", j.stdout)
	}
}

// macStores answers like a Mac whose Keychain holds Claude Code's Console key,
// its claude.ai login, Goose's secrets blob and Zed's OpenAI key. The fixtures
// are synthetic `security` outputs; {{SECRET}} becomes a fake key, standing in
// for anything secret-looking, and must never surface.
func macStores(t *testing.T, k testhome.Keys) (*credstore.Canned, func() credstore.Result) {
	t.Helper()
	fx := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "credstore", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return []byte(strings.ReplaceAll(string(b), "{{SECRET}}", k.Anthropic))
	}
	c := &credstore.Canned{Replies: map[string]credstore.Reply{
		"security find-generic-password -s Claude Code":                {Out: fx("security-generic-claude.txt")},
		"security find-generic-password -s Claude Code-credentials":    {Out: fx("security-generic-claude-credentials.txt")},
		"security find-generic-password -s goose":                      {Out: fx("security-generic-goose.txt")},
		"security find-internet-password -s https://api.openai.com/v1": {Out: fx("security-internet-zed-openai.txt")},
	}}
	return c, func() credstore.Result {
		return credstore.Lookup(context.Background(), credstore.Env{GOOS: "darwin", Run: c.Run})
	}
}

func krStores(t *testing.T, home, stdin string, stores func() credstore.Result, args ...string) run {
	t.Helper()
	a, out, errb := newApp(home, stdin, nil)
	a.stores = stores
	code := a.run(args)
	return run{code, out.String(), errb.String()}
}

func TestCredentialStoreEntriesAreByHandNamesOnly(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	canned, stores := macStores(t, k)

	m := krStores(t, home, "", stores)
	for _, want := range []string{"credential store", "macOS Keychain", "4 entries", "Claude Code-credentials", "login token, not an API key",
		"Anthropic API key · changed 2026-10-03", "/login with your Console account", "https://api.openai.com/v1",
		"Reset Key", "every provider key you gave Goose", "asked the macOS Keychain whether 13 known entries exist"} {
		if !strings.Contains(m.stdout, want) {
			t.Errorf("map: missing %q in\n%s", want, m.stdout)
		}
	}
	mj := krStores(t, home, "", stores, "--json")
	var rep struct {
		CredentialStore struct {
			Store   string            `json:"store"`
			Entries []credstore.Entry `json:"entries"`
		} `json:"credential_store"`
	}
	if err := json.Unmarshal([]byte(mj.stdout), &rep); err != nil || rep.CredentialStore.Store != "macOS Keychain" || len(rep.CredentialStore.Entries) != 4 {
		t.Fatalf("map json: %v %+v", err, rep.CredentialStore)
	}
	// --provider keeps that issuer's entries and the multi-issuer ones
	mp := krStores(t, home, "", stores, "--json", "--provider", "openai")
	if !strings.Contains(mp.stdout, "api.openai.com") || strings.Contains(mp.stdout, "Claude Code-credentials") || !strings.Contains(mp.stdout, `"Goose"`) {
		t.Errorf("--provider openai json:\n%s", mp.stdout)
	}

	dry := krStores(t, home, "", stores, "rotate", "38bb", "--dry-run")
	if !strings.Contains(dry.stdout, "may hold this key: names only") || !strings.Contains(dry.stdout, "+ 2 credential-store entries to check") ||
		strings.Contains(dry.stdout, "Claude Code-credentials") || strings.Contains(dry.stdout, "api.openai.com") {
		t.Errorf("dry run: the Anthropic plan lists Claude Code's key entry and Goose, not the login or Zed's OpenAI key:\n%s", dry.stdout)
	}
	dj := krStores(t, home, "", stores, "rotate", "38bb", "--dry-run", "--json")
	var pl struct {
		Stores []credstore.Entry `json:"credential_store"`
	}
	if err := json.Unmarshal([]byte(dj.stdout), &pl); err != nil || len(pl.Stores) != 2 {
		t.Fatalf("dry-run json: %v %s", err, dj.stdout)
	}
	ok := krStores(t, home, k.NewAnthropic, stores, "rotate", "38bb", "--offline", "--yes")
	if ok.code != 0 || !strings.Contains(ok.stdout, "Claude Code in the macOS Keychain (\"Claude Code\"), if it holds this key") || !strings.Contains(ok.stdout, "Goose in the macOS Keychain (\"goose\")") {
		t.Fatalf("rotate: the now-list must name the credential-store entries\n%s%s", ok.stdout, ok.stderr)
	}

	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"key_map","arguments":{}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"key_copies","arguments":{"fingerprint":"8e46"}}}` + "\n"
	mc := krStores(t, home, in, stores, "mcp")
	lines := strings.Split(strings.TrimSpace(mc.stdout), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "Claude Code-credentials") || !strings.Contains(lines[1], "credential_store") || !strings.Contains(lines[1], "goose") {
		t.Fatalf("mcp:\n%s", mc.stdout)
	}

	before := len(canned.Calls)
	ch := krStores(t, home, "", stores, "check", "--json", "--timeout", "1ms")
	if strings.Contains(ch.stdout, "credential_store") || len(canned.Calls) != before {
		t.Errorf("check must not ask the credential store")
	}
	no := krStores(t, home, "", stores, "--no-credstore")
	if strings.Contains(no.stdout, "credential store") || len(canned.Calls) != before {
		t.Errorf("--no-credstore still asked:\n%s", no.stdout)
	}
	for _, call := range canned.Calls {
		if strings.Contains(call, " -w") || strings.Contains(call, " -g") {
			t.Fatalf("asked for a secret value: %s", call)
		}
	}
	assertNoLeak(t, k, m.stdout, m.stderr, mj.stdout, mp.stdout, dry.stdout, dj.stdout, ok.stdout, ok.stderr, mc.stdout, ch.stdout, no.stdout)
}

func TestCredentialStoreSkippedIsSaid(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	r := krStores(t, home, "", func() credstore.Result {
		return credstore.Lookup(context.Background(), credstore.Env{GOOS: "linux", Run: (&credstore.Canned{}).Run})
	})
	if !strings.Contains(r.stdout, "credential store not asked: no D-Bus session") {
		t.Fatalf("%s", r.stdout)
	}
	// no keys in files, but a store entry: still shown
	_, stores := macStores(t, testhome.Fake())
	e := krStores(t, t.TempDir(), "", stores)
	if !strings.Contains(e.stdout, "no AI provider keys found") || !strings.Contains(e.stdout, "Claude Code-credentials") {
		t.Fatalf("empty home with Keychain entries:\n%s", e.stdout)
	}
}
