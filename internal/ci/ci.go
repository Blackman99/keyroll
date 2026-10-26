// Package ci lists GitHub Actions secrets whose names look like AI provider
// keys, so a rotation's "where else" list includes CI.
//
// Names only, by construction: GitHub's API never returns secret values (they
// are write-only), and the only commands this package runs are `gh secret
// list`, `gh repo list/view` and GET requests through `gh api`. A secret has no
// fingerprint, so keyroll cannot tell which key it holds: each match is a
// "by hand" step with the exact `gh secret set` that replaces it.
package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Blackman99/keyroll/internal/provider"
)

// Runner runs gh with args and returns its output and exit code. err is set
// only when gh could not run at all (not installed, timed out).
type Runner func(ctx context.Context, args ...string) (stdout, stderr []byte, exit int, err error)

// Exec is the real Runner: the gh on PATH, never prompting.
func Exec(ctx context.Context, args ...string) ([]byte, []byte, int, error) {
	if err := safe(args); err != nil {
		return nil, nil, -1, err
	}
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "GH_PAGER=")
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, nil, -1, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), errb.Bytes(), ee.ExitCode(), nil
	}
	if err != nil {
		return nil, nil, -1, err
	}
	return out.Bytes(), errb.Bytes(), 0, nil
}

var errUnsafe = errors.New("ci: refusing a gh command that is not a read-only listing")

// safe is the second line of defence: keyroll only ever lists. `gh api` gets
// a bare path (no -X, -f or --input, so it is always a GET); `gh secret` only
// `list`.
func safe(args []string) error {
	if len(args) < 2 {
		return errUnsafe
	}
	switch args[0] {
	case "api":
		if len(args) != 2 || strings.HasPrefix(args[1], "-") {
			return errUnsafe
		}
		return nil
	case "secret", "repo":
		ok := args[0] == "secret" && args[1] == "list" || args[0] == "repo" && (args[1] == "list" || args[1] == "view")
		if !ok {
			return errUnsafe
		}
		for _, a := range args[2:] {
			if strings.HasPrefix(a, "-") && !allowedFlag[a] {
				return errUnsafe
			}
		}
		return nil
	}
	return errUnsafe
}

var allowedFlag = map[string]bool{"--repo": true, "--env": true, "--json": true, "--limit": true}

// Errors that stop the whole lookup.
var (
	ErrNoGH     = errors.New("listing CI secrets needs the GitHub CLI: install gh (https://cli.github.com), run `gh auth login`, then try again")
	ErrAuth     = errors.New("gh is not logged in: run `gh auth login` (or set GH_TOKEN), then try again")
	ErrBadToken = errors.New("GitHub refused gh's token (HTTP 401: Bad credentials): run `gh auth login` again, or fix GH_TOKEN")
	ErrNoRepo   = errors.New("this folder is not a GitHub repository: pass --repo owner/name, --owner LOGIN or --all")
)

// Scope is where a secret is defined.
type Scope string

const (
	Repository   Scope = "repository"
	Environment  Scope = "environment"
	Organization Scope = "organization"
)

// Secret is one secret name that looks like an AI provider credential.
type Secret struct {
	Name        string        `json:"name"`
	Scope       Scope         `json:"scope"`
	Repo        string        `json:"repo,omitempty"`        // repository and environment secrets
	Environment string        `json:"environment,omitempty"` // environment secrets
	Org         string        `json:"org,omitempty"`         // organization secrets
	Repos       []string      `json:"visible_to,omitempty"`  // organization secrets: the repos asked about that can use it
	Updated     string        `json:"updated,omitempty"`     // YYYY-MM-DD
	Provider    string        `json:"provider"`
	Kind        provider.Kind `json:"kind"`
	Match       string        `json:"match"` // "known name" or "name mentions <WORD>"
	Note        string        `json:"note,omitempty"`
	Manual      string        `json:"manual"` // how to replace it by hand
}

// Where is the secret's place in words: "repository Blackman99/app",
// "environment prod of Blackman99/app", "organization acme".
func (s Secret) Where() string {
	switch s.Scope {
	case Environment:
		return "environment " + s.Environment + " of " + s.Repo
	case Organization:
		return "organization " + s.Org
	}
	return "repository " + s.Repo
}

// Repo is one repository keyroll asked about.
type Repo struct {
	Name         string   `json:"name"`
	SecretNames  int      `json:"secret_names"` // repository + environment names seen, matching or not
	Environments []string `json:"environments,omitempty"`
	OrgSecrets   bool     `json:"org_secrets_asked,omitempty"`
	Error        string   `json:"error,omitempty"` // repository secrets could not be listed
	Notes        []string `json:"notes,omitempty"` // environment / org listing problems
}

// Result is one inventory.
type Result struct {
	Login           string   `json:"login"`
	Repos           []Repo   `json:"repos"`
	Secrets         []Secret `json:"secrets"`
	OrgSecretNames  int      `json:"org_secret_names,omitempty"` // distinct organization secret names seen
	SkippedArchived int      `json:"skipped_archived,omitempty"`
	Truncated       []string `json:"truncated,omitempty"` // owners whose repo list hit --limit
	Limit           int      `json:"limit,omitempty"`     // repos per owner, when a list was truncated
}

// Readable counts the repos whose secrets could be listed.
func (r *Result) Readable() int {
	n := 0
	for _, x := range r.Repos {
		if x.Error == "" {
			n++
		}
	}
	return n
}

// Names counts every secret name seen (repo + environment, plus each org secret once).
func (r *Result) Names() int {
	n := r.OrgSecretNames
	for _, x := range r.Repos {
		n += x.SecretNames
	}
	return n
}

// For returns the secrets that may hold a key of this issuer and kind.
func (r *Result) For(providerID string, kind provider.Kind) []Secret {
	if r == nil {
		return nil
	}
	var out []Secret
	for _, s := range r.Secrets {
		if s.Provider == providerID && s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

// Options for Lookup.
type Options struct {
	Repos    []string // owner/name
	Owners   []string // every repo of these users or orgs
	All      bool     // every repo of the logged-in account
	Limit    int      // per owner (default 200)
	Archived bool     // include archived repos
	Provider string   // only this issuer
	Run      Runner
	Timeout  time.Duration // per gh call (default 20s)
}

type lookup struct {
	o   Options
	ctx context.Context
}

// gh runs one call and turns gh's failures into errors a person can act on.
func (l *lookup) gh(args ...string) ([]byte, error) {
	if err := safe(args); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(l.ctx, l.o.Timeout)
	defer cancel()
	out, errb, code, err := l.o.Run(ctx, args...)
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, ErrNoGH
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("gh %s did not answer within %s", strings.Join(args[:2], " "), l.o.Timeout)
	case err != nil:
		return nil, err
	case bytes.Contains(errb, []byte("Bad credentials")):
		return nil, ErrBadToken
	case code == 4 || bytes.Contains(errb, []byte("gh auth login")):
		return nil, ErrAuth
	case code != 0:
		return nil, errors.New(ghError(errb))
	}
	return out, nil
}

// ghError shortens gh's message ("failed to get secrets: HTTP 403: … (https://…)")
// and explains the one people hit most.
func ghError(stderr []byte) string {
	msg := strings.TrimSpace(string(stderr))
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if i := strings.LastIndex(msg, " (https://"); i > 0 {
		msg = msg[:i]
	}
	msg = strings.TrimPrefix(msg, "failed to get secrets: ")
	msg = strings.TrimPrefix(msg, "gh: ")
	switch {
	case msg == "":
		return "gh failed without saying why"
	case strings.Contains(msg, "HTTP 404"):
		return "not found, or your gh login cannot see it (HTTP 404)"
	case strings.Contains(msg, "Resource not accessible by integration"):
		return "this token cannot read secret names (HTTP 403; Actions' GITHUB_TOKEN never can: use a token with repo scope or the fine-grained \"Secrets: read\" permission)"
	case strings.Contains(msg, "HTTP 403"):
		return "no access to its secrets (HTTP 403: listing secret names needs collaborator access)"
	}
	return msg
}

// Lookup asks GitHub, through gh, which secret names look like provider keys.
func Lookup(ctx context.Context, o Options) (Result, error) {
	if o.Run == nil {
		o.Run = Exec
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.Limit == 0 {
		o.Limit = 200
	}
	l := &lookup{o: o, ctx: ctx}
	res := Result{Repos: []Repo{}, Secrets: []Secret{}}

	// who is asking: doubles as the "is gh logged in" check
	out, err := l.gh("api", "user")
	if err != nil {
		return res, err
	}
	var me struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(out, &me) != nil || me.Login == "" {
		return res, errors.New("gh api user returned something keyroll does not understand")
	}
	res.Login = me.Login

	names, err := l.repos(&res)
	if err != nil {
		return res, err
	}
	orgs := l.orgOwners(names)

	repos := make([]Repo, len(names))
	found := make([][]Secret, len(names))
	orgSeen := make([][]string, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			repos[i], found[i], orgSeen[i] = l.repo(name, orgs[owner(name)])
		}()
	}
	wg.Wait()
	res.Repos = repos

	// an org secret visible to several repos is one secret: list it once
	orgIdx := map[string]int{}
	for _, ss := range found {
		for _, s := range ss {
			if o.Provider != "" && s.Provider != o.Provider {
				continue
			}
			if s.Scope == Organization {
				k := s.Org + "/" + s.Name
				if i, ok := orgIdx[k]; ok {
					res.Secrets[i].Repos = append(res.Secrets[i].Repos, s.Repos...)
					continue
				}
				orgIdx[k] = len(res.Secrets)
			}
			res.Secrets = append(res.Secrets, s)
		}
	}
	// an org secret every repo can see is still one name
	orgNames := map[string]bool{}
	for _, ns := range orgSeen {
		for _, n := range ns {
			orgNames[n] = true
		}
	}
	res.OrgSecretNames = len(orgNames)
	return res, nil
}

func owner(repo string) string {
	o, _, _ := strings.Cut(repo, "/")
	return o
}

// repos resolves which repositories to ask about.
func (l *lookup) repos(res *Result) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	for _, r := range l.o.Repos {
		add(strings.TrimSuffix(strings.TrimPrefix(r, "https://github.com/"), ".git"))
	}
	owners := append([]string{}, l.o.Owners...)
	if l.o.All {
		owners = append(owners, "") // gh repo list with no owner: the logged-in account
	}
	for _, ow := range owners {
		args := []string{"repo", "list"}
		if ow != "" {
			args = append(args, ow)
		}
		args = append(args, "--limit", fmt.Sprint(l.o.Limit), "--json", "nameWithOwner,isArchived")
		b, err := l.gh(args...)
		if err != nil {
			who := ow
			if who == "" {
				who = res.Login
			}
			if errors.Is(err, ErrAuth) || errors.Is(err, ErrNoGH) {
				return nil, err
			}
			return nil, fmt.Errorf("could not list the repos of %s: %v", who, err)
		}
		var list []struct {
			NameWithOwner string `json:"nameWithOwner"`
			IsArchived    bool   `json:"isArchived"`
		}
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("gh repo list returned something keyroll does not understand: %v", err)
		}
		if len(list) >= l.o.Limit {
			who := ow
			if who == "" {
				who = res.Login
			}
			res.Truncated = append(res.Truncated, who)
			res.Limit = l.o.Limit
		}
		for _, r := range list {
			if r.IsArchived && !l.o.Archived {
				res.SkippedArchived++
				continue
			}
			add(r.NameWithOwner)
		}
	}
	if len(l.o.Repos) == 0 && len(owners) == 0 {
		b, err := l.gh("repo", "view", "--json", "nameWithOwner")
		if err != nil {
			if errors.Is(err, ErrAuth) || errors.Is(err, ErrNoGH) {
				return nil, err
			}
			return nil, ErrNoRepo
		}
		var v struct {
			NameWithOwner string `json:"nameWithOwner"`
		}
		if json.Unmarshal(b, &v) != nil || v.NameWithOwner == "" {
			return nil, ErrNoRepo
		}
		add(v.NameWithOwner)
	}
	return out, nil
}

// orgOwners asks, once per owner, whether it is an organization (only those
// have organization secrets; asking a user's repo for them is an HTTP 422).
func (l *lookup) orgOwners(repos []string) map[string]bool {
	orgs := map[string]bool{}
	asked := map[string]bool{}
	for _, r := range repos {
		ow := owner(r)
		if asked[ow] {
			continue
		}
		asked[ow] = true
		b, err := l.gh("api", "users/"+ow)
		if err != nil {
			continue
		}
		var u struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(b, &u) == nil && u.Type == "Organization" {
			orgs[ow] = true
		}
	}
	return orgs
}

type ghSecret struct {
	Name      string `json:"name"`
	UpdatedAt string `json:"updatedAt"`
	UpdatedAP string `json:"updated_at"` // REST spelling (gh api)
}

func (g ghSecret) updated() string {
	u := g.UpdatedAt
	if u == "" {
		u = g.UpdatedAP
	}
	if len(u) >= 10 {
		return u[:10]
	}
	return ""
}

// repo lists one repository's secret names: repository, each environment's,
// and (for organization repos) the org secrets it can use.
func (l *lookup) repo(name string, isOrg bool) (Repo, []Secret, []string) {
	r := Repo{Name: name}
	var out []Secret
	var orgNames []string
	b, err := l.gh("secret", "list", "--repo", name, "--json", "name,updatedAt")
	if err != nil {
		r.Error = err.Error()
		return r, nil, nil
	}
	var list []ghSecret
	if err := json.Unmarshal(b, &list); err != nil {
		r.Error = "gh secret list returned something keyroll does not understand"
		return r, nil, nil
	}
	r.SecretNames += len(list)
	for _, g := range list {
		if s, ok := match(g, Secret{Scope: Repository, Repo: name}); ok {
			out = append(out, s)
		}
	}

	if b, err := l.gh("api", "repos/"+name+"/environments?per_page=100"); err != nil {
		if !strings.Contains(err.Error(), "HTTP 404") { // no environments feature on this repo's plan
			r.Notes = append(r.Notes, "environments not listed: "+err.Error())
		}
	} else {
		var envs struct {
			Environments []struct {
				Name string `json:"name"`
			} `json:"environments"`
		}
		_ = json.Unmarshal(b, &envs)
		for _, e := range envs.Environments {
			r.Environments = append(r.Environments, e.Name)
			b, err := l.gh("secret", "list", "--env", e.Name, "--repo", name, "--json", "name,updatedAt")
			if err != nil {
				r.Notes = append(r.Notes, "environment "+e.Name+": "+err.Error())
				continue
			}
			var list []ghSecret
			_ = json.Unmarshal(b, &list)
			r.SecretNames += len(list)
			for _, g := range list {
				if s, ok := match(g, Secret{Scope: Environment, Repo: name, Environment: e.Name}); ok {
					out = append(out, s)
				}
			}
		}
	}

	if isOrg {
		r.OrgSecrets = true
		if b, err := l.gh("api", "repos/"+name+"/actions/organization-secrets?per_page=100"); err != nil {
			r.Notes = append(r.Notes, "organization secrets not listed: "+err.Error())
		} else {
			var org struct {
				Secrets []ghSecret `json:"secrets"`
			}
			_ = json.Unmarshal(b, &org)
			for _, g := range org.Secrets {
				orgNames = append(orgNames, owner(name)+"/"+g.Name)
				if s, ok := match(g, Secret{Scope: Organization, Org: owner(name), Repos: []string{name}}); ok {
					out = append(out, s)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return scopeRank(out[i].Scope) < scopeRank(out[j].Scope) })
	return r, out, orgNames
}

func scopeRank(s Scope) int {
	switch s {
	case Repository:
		return 0
	case Environment:
		return 1
	}
	return 2
}

// notes for names that are known but not only used for AI keys
var nameNotes = map[string]string{
	"GOOGLE_API_KEY": "other Google APIs (Maps, YouTube) use this name too: make sure it is the Gemini key",
}

func match(g ghSecret, s Secret) (Secret, bool) {
	m, ok := provider.ForName(g.Name)
	if !ok {
		return s, false
	}
	s.Name, s.Updated = g.Name, g.updated()
	s.Provider, s.Kind = m.Provider.ID, m.Kind
	s.Match = "known name"
	if !m.Known {
		s.Match = "name mentions " + m.Word
	}
	s.Note = nameNotes[strings.ToUpper(g.Name)]
	s.Manual = manual(s)
	return s, true
}

func manual(s Secret) string {
	set := ""
	switch s.Scope {
	case Repository:
		set = "gh secret set " + s.Name + " --repo " + s.Repo
	case Environment:
		set = "gh secret set " + s.Name + " --env " + s.Environment + " --repo " + s.Repo
	case Organization:
		// `gh secret set --org` resets the secret's repository access to its
		// --visibility default; the settings page keeps it as it is
		set = "an org admin updates it at https://github.com/organizations/" + s.Org + "/settings/secrets/actions (keeps its repository access)"
	}
	if s.Kind == provider.KindLogin {
		if s.Provider == "anthropic" {
			return "only if it leaked: run `claude setup-token` for a new one, then " + set
		}
		return "only if it leaked: log in again for a new token, then " + set
	}
	return set
}

// Canned is a Runner for tests: each gh command line ("secret list --repo
// a/b --json name,updatedAt") gets recorded output and an exit code. Unknown
// calls answer like gh does for a missing resource (HTTP 404, exit 1).
type Canned struct {
	Replies map[string]Reply
	Missing bool // gh is not installed
	Calls   []string
	mu      sync.Mutex
}

// Reply is one canned answer.
type Reply struct {
	Out, Err string
	Exit     int
}

// Run implements Runner.
func (c *Canned) Run(_ context.Context, args ...string) ([]byte, []byte, int, error) {
	line := strings.Join(args, " ")
	c.mu.Lock()
	c.Calls = append(c.Calls, line)
	c.mu.Unlock()
	if err := safe(args); err != nil {
		return nil, nil, -1, err
	}
	if c.Missing {
		return nil, nil, -1, exec.ErrNotFound
	}
	if r, ok := c.Replies[line]; ok {
		return []byte(r.Out), []byte(r.Err), r.Exit, nil
	}
	return nil, []byte("gh: Not Found (HTTP 404)\n"), 1, nil
}

// Fixtures answers like gh for the made-up account in testdata: octo-demo
// (a user) with agent-bot, site and an archived repo, and the acme-labs
// organization's web and api repos. dir is the path to internal/ci/testdata.
func Fixtures(dir string) *Canned {
	f := func(name string) string {
		b, err := os.ReadFile(dir + "/" + name)
		if err != nil {
			panic(err)
		}
		return string(b)
	}
	const js = " --json name,updatedAt"
	return &Canned{Replies: map[string]Reply{
		"api user":                       {Out: f("api-user.json")},
		"api users/octo-demo":            {Out: f("api-users-octo-demo.json")},
		"api users/acme-labs":            {Out: f("api-users-acme-labs.json")},
		"repo view --json nameWithOwner": {Out: f("repo-view.json")},
		"repo list octo-demo --limit 200 --json nameWithOwner,isArchived": {Out: f("repo-list-octo-demo.json")},
		"repo list --limit 200 --json nameWithOwner,isArchived":           {Out: f("repo-list-octo-demo.json")},

		"secret list --repo octo-demo/agent-bot" + js:                       {Out: f("secret-list-agent-bot.json")},
		"api repos/octo-demo/agent-bot/environments?per_page=100":           {Out: f("environments-agent-bot.json")},
		"secret list --env github-pages --repo octo-demo/agent-bot" + js:    {Out: f("secret-list-empty.json")},
		"secret list --env production --repo octo-demo/agent-bot" + js:      {Out: f("secret-list-agent-bot-production.json")},
		"secret list --repo octo-demo/site" + js:                            {Out: f("secret-list-site.json")},
		"api repos/octo-demo/site/environments?per_page=100":                {Out: f("environments-none.json")},
		"secret list --repo acme-labs/web" + js:                             {Out: f("secret-list-acme-web.json")},
		"api repos/acme-labs/web/environments?per_page=100":                 {Out: f("environments-none.json")},
		"api repos/acme-labs/web/actions/organization-secrets?per_page=100": {Out: f("org-secrets-acme.json")},
		"secret list --repo acme-labs/api" + js:                             {Out: f("secret-list-empty.json")},
		"api repos/acme-labs/api/actions/organization-secrets?per_page=100": {Out: f("org-secrets-acme.json")},
		// acme-labs/api has no environments endpoint answer: gh's HTTP 404, read as "none"
		"secret list --repo someone/else" + js: {Err: f("secret-list-403.txt"), Exit: 1},
	}}
}
