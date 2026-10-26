package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Blackman99/keyroll/internal/ci"
	"github.com/Blackman99/keyroll/internal/testhome"
)

const fixtures = "../../internal/ci/testdata"

func krGH(t *testing.T, home, stdin string, gh *ci.Canned, args ...string) run {
	t.Helper()
	a, out, errb := newApp(home, stdin, nil)
	a.gh = gh.Run
	code := a.run(args)
	return run{code, out.String(), errb.String()}
}

func TestCIInventory(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	fp := anthropicFP(t, home)
	gh := ci.Fixtures(fixtures)
	args := []string{"ci", "--repo", "octo-demo/agent-bot", "--repo", "https://github.com/octo-demo/site.git", "--repo", "acme-labs/web", "--repo", "acme-labs/api"}
	r := krGH(t, home, "", gh, args...)
	if r.code != exitOK {
		t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{
		"7 of 12 secret names in 4 repos look like AI provider keys",
		"octo-demo/agent-bot", "ANTHROPIC_API_KEY", "Anthropic API key",
		"CLAUDE_CODE_OAUTH_TOKEN", "Claude Code login token, not an API key",
		"env production", "OPENROUTER_API_KEY", "updated 2026-10-12",
		"gh secret set ANTHROPIC_API_KEY --repo octo-demo/agent-bot",
		"acme-labs", "organization secrets, usable by acme-labs/web, acme-labs/api",
		"PROD_ANTHROPIC_KEY", "probably Anthropic API key (the name mentions ANTHROPIC)",
		"https://github.com/organizations/acme-labs/settings/secrets/actions",
		// the fixture home has exactly one Anthropic key: the suggestion is concrete
		"next  keyroll rotate " + fp + " --ci octo-demo/agent-bot --ci acme-labs/web",
		"asked GitHub, as octo-demo, for the secret names of 4 repos, 2 environments and 1 organization",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("missing %q in\n%s", want, r.stdout)
		}
	}
	for _, never := range []string{"DOCKERHUB_TOKEN", "NPM_TOKEN", "NETLIFY_AUTH_TOKEN", "SENTRY_DSN", "DATABASE_URL"} {
		if strings.Contains(r.stdout, never) {
			t.Errorf("%s is not an AI provider key but is listed:\n%s", never, r.stdout)
		}
	}

	j := krGH(t, home, "", ci.Fixtures(fixtures), append(args, "--json")...)
	var res ci.Result
	if err := json.Unmarshal([]byte(j.stdout), &res); err != nil || j.code != exitOK {
		t.Fatalf("json: %v %d\n%s", err, j.code, j.stdout)
	}
	if len(res.Secrets) != 7 || res.Names() != 12 || res.Login != "octo-demo" {
		t.Fatalf("json: %d secrets, %d names, login %q", len(res.Secrets), res.Names(), res.Login)
	}
	for _, field := range []string{`"value"`, `"secret":`} {
		if strings.Contains(j.stdout, field) {
			t.Errorf("JSON has a %s field: names only", field)
		}
	}
	p := krGH(t, home, "", ci.Fixtures(fixtures), append(args, "--provider", "openai")...)
	if !strings.Contains(p.stdout, "1 of 12 secret names") || strings.Contains(p.stdout, "ANTHROPIC") {
		t.Errorf("--provider openai:\n%s", p.stdout)
	}
	assertNoLeak(t, k, r.stdout, r.stderr, j.stdout, j.stderr, p.stdout)
}

func TestCIDefaultRepoAndPositional(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	gh := ci.Fixtures(fixtures)
	r := krGH(t, home, "", gh, "ci")
	if r.code != exitOK || !strings.Contains(r.stdout, "octo-demo/agent-bot") || !strings.Contains(r.stdout, "secret names of 1 repo and") {
		t.Fatalf("default repo: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	r = krGH(t, home, "", ci.Fixtures(fixtures), "ci", "octo-demo")
	if r.code != exitOK || !strings.Contains(r.stdout, "skipped 1 archived repo") || !strings.Contains(r.stdout, "secret names of 2 repos") {
		t.Fatalf("owner as argument: %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestCIFailsHelpfully(t *testing.T) {
	home := t.TempDir()
	testhome.Build(home)
	fp := anthropicFP(t, home)
	read := func(name string) string { return readFixture(t, name) }
	loggedOut := ci.Fixtures(fixtures)
	loggedOut.Replies["api user"] = ci.Reply{Err: read("not-logged-in.txt"), Exit: 4}
	notGit := ci.Fixtures(fixtures)
	notGit.Replies["repo view --json nameWithOwner"] = ci.Reply{Err: read("repo-view-not-git.txt"), Exit: 1}
	for _, c := range []struct {
		name string
		gh   *ci.Canned
		args []string
		code int
		want string
	}{
		{"gh missing", &ci.Canned{Missing: true}, []string{"ci"}, exitRuntime, "install gh (https://cli.github.com)"},
		{"logged out", loggedOut, []string{"ci"}, exitRuntime, "gh auth login"},
		{"not a repo", notGit, []string{"ci"}, exitUsage, "pass --repo owner/name"},
		{"no access", ci.Fixtures(fixtures), []string{"ci", "--repo", "someone/else"}, exitRuntime, "someone/else"},
		{"rotate --ci logged out", loggedOut, []string{"rotate", fp, "--dry-run", "--ci", "octo-demo/agent-bot"}, exitRuntime, "drop --ci"},
		{"rotate --ci missing gh", &ci.Canned{Missing: true}, []string{"rotate", fp, "--ci", "octo-demo/agent-bot"}, exitRuntime, "nothing was changed"},
	} {
		r := krGH(t, home, "", c.gh, c.args...)
		if r.code != c.code || !strings.Contains(r.stdout+r.stderr, c.want) {
			t.Errorf("%s: exit %d (want %d), want %q in\n%s%s", c.name, r.code, c.code, c.want, r.stdout, r.stderr)
		}
	}
	// a failed --ci lookup stops before anything is written
	after := kr(t, home, "", nil, "--json")
	if !strings.Contains(after.stdout, fp) {
		t.Fatal("rotate --ci with a failing gh changed the files")
	}
}

func TestRotateWithCISecrets(t *testing.T) {
	home := t.TempDir()
	k := testhome.Build(home)
	fp := anthropicFP(t, home)
	args := []string{"rotate", fp, "--ci", "octo-demo/agent-bot", "--ci", "acme-labs/web"}
	d := krGH(t, home, "", ci.Fixtures(fixtures), append(args, "--dry-run")...)
	for _, want := range []string{"CI secrets", "ANTHROPIC_API_KEY", "repository octo-demo/agent-bot", "PROD_ANTHROPIC_KEY",
		"gh secret set ANTHROPIC_API_KEY --repo octo-demo/agent-bot", "+ 2 CI secrets to check"} {
		if !strings.Contains(d.stdout, want) {
			t.Errorf("dry run: missing %q in\n%s", want, d.stdout)
		}
	}
	// a login token is not this API key: it is not in the API key's plan
	if strings.Contains(d.stdout, "CLAUDE_CODE_OAUTH_TOKEN") || strings.Contains(d.stdout, "OPENAI_API_KEY") {
		t.Errorf("plan lists secrets of another kind or issuer:\n%s", d.stdout)
	}
	j := krGH(t, home, "", ci.Fixtures(fixtures), append(args, "--dry-run", "--json")...)
	var pl struct {
		CI []ci.Secret `json:"ci_secrets"`
	}
	if err := json.Unmarshal([]byte(j.stdout), &pl); err != nil || len(pl.CI) != 2 {
		t.Fatalf("json plan: %v %d\n%s", err, len(pl.CI), j.stdout)
	}
	plain := kr(t, home, "", nil, "rotate", fp, "--dry-run")
	if !strings.Contains(plain.stdout, "add --ci owner/name") {
		t.Errorf("a plan without --ci must say CI secrets are not in it:\n%s", plain.stdout)
	}

	a := krGH(t, home, k.NewAnthropic+"\n", ci.Fixtures(fixtures), append(args, "--yes", "--offline")...)
	if a.code != exitOK {
		t.Fatalf("apply: %d\n%s%s", a.code, a.stdout, a.stderr)
	}
	if flat := strings.Join(strings.Fields(a.stdout), " "); !strings.Contains(flat, "GitHub Actions secret ANTHROPIC_API_KEY in repository octo-demo/agent-bot, if it holds this key") ||
		!strings.Contains(flat, "GitHub Actions secret PROD_ANTHROPIC_KEY in organization acme-labs") {
		t.Errorf("applied: CI secrets missing from the by-hand list\n%s", a.stdout)
	}
	assertNoLeak(t, k, d.stdout, d.stderr, j.stdout, a.stdout, a.stderr)
}

func TestCIUsageErrors(t *testing.T) {
	home := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"ci", "--repo", "nope"}, "give it as owner/name"},
		{[]string{"ci", "--owner", "a/b"}, "for one repository use --repo"},
		{[]string{"ci", "--limit", "0"}, "--limit must be between 1 and 1000"},
		{[]string{"ci", "--root", "x"}, "that is a `keyroll map` option"},
		{[]string{"ci", "--repoo", "a/b"}, "did you mean --repo?"},
		{[]string{"ci", "some thing"}, "pass a repository as owner/name"},
		{[]string{"map", "--all"}, "that is a `keyroll ci` option"},
		{[]string{"check", "--ci", "a/b"}, "that is a `keyroll rotate` option"},
		{[]string{"rotate", "abcd1234", "--ci", "x"}, ""},
		{[]string{"cii"}, "did you mean `keyroll ci`?"},
	} {
		r := krGH(t, home, "", &ci.Canned{Missing: true}, c.args...)
		if r.code != exitUsage || !strings.Contains(r.stderr, c.want) {
			t.Errorf("%v: exit %d, want %q in %q", c.args, r.code, c.want, r.stderr)
		}
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := readFile(fixtures + "/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func anthropicFP(t *testing.T, home string) string {
	t.Helper()
	m := kr(t, home, "", nil, "--json", "--provider", "anthropic")
	var rep struct {
		Keys []struct{ Fingerprint string } `json:"keys"`
	}
	if err := json.Unmarshal([]byte(m.stdout), &rep); err != nil || len(rep.Keys) == 0 {
		t.Fatalf("no anthropic key in the fixture home: %v", err)
	}
	return rep.Keys[0].Fingerprint
}
