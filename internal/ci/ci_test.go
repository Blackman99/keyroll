package ci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Blackman99/keyroll/internal/provider"
)

func run(t *testing.T, c *Canned, o Options) (Result, error) {
	t.Helper()
	o.Run = c.Run
	return Lookup(context.Background(), o)
}

func TestInventory(t *testing.T) {
	c := Fixtures("testdata")
	r, err := run(t, c, Options{Repos: []string{"octo-demo/agent-bot", "acme-labs/web", "acme-labs/api", "octo-demo/site"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Login != "octo-demo" || len(r.Repos) != 4 || r.Readable() != 4 {
		t.Fatalf("%+v", r)
	}
	got := []string{}
	for _, s := range r.Secrets {
		got = append(got, string(s.Scope)+":"+s.Name+":"+s.Provider+":"+string(s.Kind))
	}
	want := []string{
		"repository:ANTHROPIC_API_KEY:anthropic:api",
		"repository:CLAUDE_CODE_OAUTH_TOKEN:anthropic:login",
		"repository:OPENAI_API_KEY:openai:api",
		"environment:OPENROUTER_API_KEY:openrouter:api",
		"repository:GEMINI_API_KEY:google:api",
		"repository:GOOGLE_API_KEY:google:api",
		"organization:PROD_ANTHROPIC_KEY:anthropic:api",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	org := r.Secrets[6]
	if strings.Join(org.Repos, ",") != "acme-labs/web,acme-labs/api" || org.Match != "name mentions ANTHROPIC" || !strings.Contains(org.Manual, "/organizations/acme-labs/settings/secrets/actions") {
		t.Errorf("org secret: %+v", org)
	}
	if r.Secrets[3].Manual != "gh secret set OPENROUTER_API_KEY --env production --repo octo-demo/agent-bot" || r.Secrets[3].Updated != "2026-10-12" {
		t.Errorf("env secret: %+v", r.Secrets[3])
	}
	if !strings.Contains(r.Secrets[1].Manual, "claude setup-token") {
		t.Errorf("login token: %+v", r.Secrets[1])
	}
	if r.Secrets[5].Note == "" {
		t.Error("GOOGLE_API_KEY needs its caveat")
	}
	// 5 + 2 (production) + 0 (github-pages) + 1 + 2 + 0 repo/env names, plus 2 org names counted once
	if r.Names() != 12 {
		t.Errorf("names: %d", r.Names())
	}
	if len(r.Repos[0].Environments) != 2 || r.Repos[0].OrgSecrets || !r.Repos[1].OrgSecrets || len(r.Repos[2].Notes) != 0 {
		t.Errorf("repos: %+v", r.Repos)
	}
	if got := r.For("anthropic", provider.KindAPI); len(got) != 2 {
		t.Errorf("For(anthropic): %+v", got)
	}
	// one users/ call per owner, not per repo
	n := 0
	for _, call := range c.Calls {
		if strings.HasPrefix(call, "api users/") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("asked owner type %d times", n)
	}
}

func TestOwnerAndDefaultRepo(t *testing.T) {
	r, err := run(t, Fixtures("testdata"), Options{Owners: []string{"octo-demo"}, Provider: "openai"})
	if err != nil || len(r.Repos) != 2 || r.SkippedArchived != 1 || len(r.Secrets) != 1 || r.Secrets[0].Name != "OPENAI_API_KEY" {
		t.Fatalf("%v %+v", err, r)
	}
	c := Fixtures("testdata")
	c.Replies["repo list --limit 3 --json nameWithOwner,isArchived"] = Reply{Out: readFile(t, "repo-list-octo-demo.json")}
	r, err = run(t, c, Options{All: true, Archived: true, Limit: 3})
	if err != nil || len(r.Repos) != 3 || len(r.Truncated) != 1 || r.Truncated[0] != "octo-demo" {
		t.Fatalf("--all --archived: %v %+v", err, r)
	}
	r, err = run(t, Fixtures("testdata"), Options{})
	if err != nil || len(r.Repos) != 1 || r.Repos[0].Name != "octo-demo/agent-bot" {
		t.Fatalf("default repo: %v %+v", err, r)
	}
}

func TestFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *Canned
		o    Options
		want error
		msg  string
	}{
		{"no gh", &Canned{Missing: true}, Options{}, ErrNoGH, "install gh"},
		{"logged out", &Canned{Replies: map[string]Reply{"api user": {Err: readFile(t, "not-logged-in.txt"), Exit: 4}}}, Options{}, ErrAuth, "gh auth login"},
		{"bad token", &Canned{Replies: map[string]Reply{"api user": {Err: "gh: Bad credentials (HTTP 401)\n", Exit: 1}}}, Options{}, ErrBadToken, "gh auth login"},
		{"not a repo", func() *Canned {
			c := Fixtures("testdata")
			c.Replies["repo view --json nameWithOwner"] = Reply{Err: readFile(t, "repo-view-not-git.txt"), Exit: 1}
			return c
		}(), Options{}, ErrNoRepo, "--repo owner/name"},
	} {
		_, err := run(t, tc.c, tc.o)
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestUnreadableRepoIsReportedNotFatal(t *testing.T) {
	c := Fixtures("testdata")
	c.Replies["secret list --repo octo-demo/site --json name,updatedAt"] = Reply{Err: readFile(t, "secret-list-integration-403.txt"), Exit: 1}
	r, err := run(t, c, Options{Repos: []string{"octo-demo/agent-bot", "someone/else", "octo-demo/site", "octo-demo/gone"}})
	if err != nil {
		t.Fatal(err)
	}
	errs := map[string]string{}
	for _, x := range r.Repos {
		errs[x.Name] = x.Error
	}
	if errs["octo-demo/agent-bot"] != "" || !strings.Contains(errs["someone/else"], "collaborator access") ||
		!strings.Contains(errs["octo-demo/site"], "GITHUB_TOKEN never can") || !strings.Contains(errs["octo-demo/gone"], "HTTP 404") || r.Readable() != 1 {
		t.Fatalf("%+v", r.Repos)
	}
}

func TestOnlyReadOnlyCommands(t *testing.T) {
	for _, bad := range [][]string{
		{"secret", "set", "X", "--repo", "a/b"},
		{"secret", "delete", "X"},
		{"api", "-X", "DELETE", "repos/a/b"},
		{"api", "repos/a/b/actions/secrets/X", "-f", "encrypted_value=1"},
		{"api", "--method", "PUT"},
		{"repo", "delete", "a/b"},
		{"repo", "list", "--web"},
		{"auth", "token"},
		{"secret"},
	} {
		if safe(bad) == nil {
			t.Errorf("allowed: gh %s", strings.Join(bad, " "))
		}
	}
	c := Fixtures("testdata")
	if _, err := run(t, c, Options{All: true}); err != nil {
		t.Fatal(err)
	}
	for _, call := range c.Calls {
		if safe(strings.Fields(call)) != nil {
			t.Errorf("Lookup ran a command safe() refuses: %s", call)
		}
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
