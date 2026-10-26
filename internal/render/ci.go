package render

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Blackman99/keyroll/internal/ci"
	"github.com/Blackman99/keyroll/internal/provider"
)

// CIHolds says what a CI secret's name suggests it holds.
func CIHolds(c ci.Secret) string {
	name := c.Provider
	if p := provider.ByID(c.Provider); p != nil {
		name = p.Name
	}
	var h string
	switch c.Kind {
	case provider.KindLogin:
		h = name + " login token, not an API key"
		if c.Name == "CLAUDE_CODE_OAUTH_TOKEN" {
			h = "Claude Code login token, not an API key"
		}
	case provider.KindAdmin:
		h = name + " admin key"
	default:
		h = name + " API key"
	}
	if strings.HasPrefix(c.Match, "name mentions ") {
		h = "probably " + h + " (the name mentions " + strings.TrimPrefix(c.Match, "name mentions ") + ")"
	}
	return h
}

func ciScope(c ci.Secret) string {
	switch c.Scope {
	case ci.Environment:
		return "env " + c.Environment
	case ci.Organization:
		return "org " + c.Org
	}
	return "repository"
}

// CINext is one suggested rotation: the command and what it adds.
type CINext struct {
	Provider    string // issuer id
	Fingerprint string // "" when this machine has no single key of that issuer
	Keys        int    // how many keys of that issuer keyroll found here
}

// CI prints the CI secrets inventory. next maps an issuer to the local key a
// rotation would start from.
func CI(w io.Writer, s Style, r *ci.Result, next map[string]CINext) {
	repos := plural(len(r.Repos), "repo", "repos")
	names := plural(r.Names(), "secret name", "secret names")
	switch {
	case r.Readable() == 0:
		what := "any of the " + repos
		if len(r.Repos) == 1 {
			what = r.Repos[0].Name
		}
		fmt.Fprintf(w, "%s  could not list the secrets of %s\n\n", s.c(s.Bold, "keyroll ci"), what)
	case r.Names() == 0:
		fmt.Fprintf(w, "%s  no Actions secrets in %s %s\n\n", s.c(s.Bold, "keyroll ci"), map[bool]string{true: r.Repos[0].Name, false: repos}[len(r.Repos) == 1], s.c(s.Dim, "· names only, GitHub never returns values"))
	case len(r.Secrets) == 0:
		fmt.Fprintf(w, "%s  none of the %s in %s look like AI provider keys %s\n\n", s.c(s.Bold, "keyroll ci"), names, repos, s.c(s.Dim, "· names only, GitHub never returns values"))
	default:
		fmt.Fprintf(w, "%s  %d of %s in %s look like AI provider keys %s\n\n", s.c(s.Bold, "keyroll ci"), len(r.Secrets), names, repos, s.c(s.Dim, "· names only, GitHub never returns values"))
	}

	scopeW, nameW := 10, 0
	for _, c := range r.Secrets {
		scopeW = max(scopeW, min(22, utf8.RuneCountInString(ciScope(c))))
		nameW = max(nameW, min(32, utf8.RuneCountInString(c.Name)))
	}
	indent := 6 + scopeW + 1
	line := func(c ci.Secret) {
		holds := CIHolds(c)
		if c.Updated != "" {
			holds += " · updated " + c.Updated
		}
		head := fmt.Sprintf("      %s %s", s.c(s.Dim, pad(fit(ciScope(c), scopeW), scopeW)), s.c(s.Bold, pad(fit(c.Name, nameW), nameW)))
		if col := indent + nameW + 2; col+utf8.RuneCountInString(holds) <= s.Width {
			fmt.Fprintf(w, "%s  %s\n", head, s.c(s.Dim, holds))
		} else {
			// too wide for one line: what it holds goes under the name
			fmt.Fprintf(w, "%s\n%s%s\n", strings.TrimRight(head, " "), strings.Repeat(" ", indent), s.c(s.Dim, hang(holds, indent, s.Width)))
		}
		if c.Note != "" {
			fmt.Fprintf(w, "%s%s\n", strings.Repeat(" ", indent), s.c(s.Yellow, hang(c.Note, indent, s.Width)))
		}
		fmt.Fprintf(w, "%s%s\n", strings.Repeat(" ", indent), s.c(s.Blue, "→ "+hang(c.Manual, indent+2, s.Width)))
	}
	// repository and environment secrets under their repo, org secrets under their org
	var order []string
	group := map[string][]ci.Secret{}
	for _, c := range r.Secrets {
		k := c.Repo
		if c.Scope == ci.Organization {
			k = "org:" + c.Org
		}
		if _, ok := group[k]; !ok {
			order = append(order, k)
		}
		group[k] = append(group[k], c)
	}
	for _, k := range order {
		if org, ok := strings.CutPrefix(k, "org:"); ok {
			fmt.Fprintf(w, "  %s %s\n", s.c(s.Bold, org), s.c(s.Dim, hang("organization secrets, usable by "+strings.Join(group[k][0].Repos, ", "), 3+utf8.RuneCountInString(org), s.Width)))
		} else {
			fmt.Fprintf(w, "  %s\n", s.c(s.Bold, k))
		}
		for _, c := range group[k] {
			line(c)
		}
		fmt.Fprintln(w)
	}

	// next: the rotation that picks these up, per issuer with API-key secrets
	var provs []string
	reposFor := map[string][]string{}
	counts := map[string]int{}
	for _, c := range r.Secrets {
		if c.Kind != provider.KindAPI {
			continue
		}
		repo := c.Repo
		if c.Scope == ci.Organization {
			repo = c.Repos[0]
		}
		if _, ok := reposFor[c.Provider]; !ok {
			provs = append(provs, c.Provider)
		}
		counts[c.Provider]++
		if !contains(reposFor[c.Provider], repo) {
			reposFor[c.Provider] = append(reposFor[c.Provider], repo)
		}
	}
	for i, id := range provs {
		n := next[id]
		fp := n.Fingerprint
		if fp == "" {
			fp = "<fp>"
		}
		cmd := "keyroll rotate " + fp
		for _, repo := range reposFor[id] {
			cmd += " --ci " + repo
		}
		label := "        "
		if i == 0 {
			label = "  " + s.c(s.Bold, "next") + "  "
		}
		name := id
		if p := provider.ByID(id); p != nil {
			name = p.Name
		}
		why := fmt.Sprintf("rotating your %s key lists these %d secrets as by-hand steps", name, counts[id])
		switch {
		case n.Keys == 0:
			why = fmt.Sprintf("no %s key on this machine: if the secret holds a leaked key, revoke it at the issuer and `gh secret set` the new one", name)
		case n.Keys > 1:
			why = fmt.Sprintf("%d %s keys here: pick the fingerprint with `keyroll --provider %s`", n.Keys, name, id)
		}
		if counts[id] == 1 && n.Keys == 1 {
			why = fmt.Sprintf("rotating your %s key lists this secret as a by-hand step", name)
		}
		fmt.Fprintf(w, "%s%s\n", label, s.c(s.Bold, cmd))
		fmt.Fprintf(w, "        %s\n", s.c(s.Dim, hang(why, 8, s.Width)))
	}
	if len(r.Secrets) > 0 {
		if len(provs) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "  %s\n", s.c(s.Dim, hang("a secret has no fingerprint, so keyroll cannot tell which key it holds: replace it when you rotate that issuer's key. `gh secret set` asks for the new value, or reads it from stdin", 2, s.Width)))
	}

	// what was asked, and what could not be
	envs := 0
	orgs := map[string]bool{}
	for _, x := range r.Repos {
		envs += len(x.Environments)
		if x.OrgSecrets {
			orgs[strings.SplitN(x.Name, "/", 2)[0]] = true
		}
	}
	asked := []string{repos}
	if envs > 0 {
		asked = append(asked, plural(envs, "environment", "environments"))
	}
	if len(orgs) > 0 {
		asked = append(asked, plural(len(orgs), "organization", "organizations"))
	}
	list := strings.Join(asked, ", ")
	if n := len(asked); n > 1 {
		list = strings.Join(asked[:n-1], ", ") + " and " + asked[n-1]
	}
	fmt.Fprintf(w, "  %s\n", s.c(s.Dim, hang(fmt.Sprintf("asked GitHub, as %s, for the secret names of %s", r.Login, list), 2, s.Width)))
	if r.SkippedArchived > 0 {
		fmt.Fprintf(w, "%s\n", s.c(s.Dim, fmt.Sprintf("  skipped %s (--archived includes them)", plural(r.SkippedArchived, "archived repo", "archived repos"))))
	}
	for _, o := range r.Truncated {
		fmt.Fprintf(w, "  %s\n", s.c(s.Yellow, hang(fmt.Sprintf("%s has %d or more repos and only the first %d were asked about: raise --limit for the rest", o, r.Limit, r.Limit), 4, s.Width)))
	}
	bad := 0
	for _, x := range r.Repos {
		if x.Error != "" {
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(w, "%s\n", s.c(s.Yellow, "  could not read "+plural(bad, "repo", "repos")+":"))
		for _, x := range r.Repos {
			if x.Error != "" {
				fmt.Fprintf(w, "    %s  %s\n", x.Name, s.c(s.Dim, hang(x.Error, 6+utf8.RuneCountInString(x.Name), s.Width)))
			}
		}
	}
	for _, x := range r.Repos {
		for _, n := range x.Notes {
			fmt.Fprintf(w, "  %s\n", s.c(s.Yellow, hang(x.Name+": "+n, 4, s.Width)))
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// hang wraps text that starts at column col so that no line passes width,
// continuing each line at col.
func hang(text string, col, width int) string {
	return strings.ReplaceAll(wrap(text, max(30, width-col), 0), "\n", "\n"+strings.Repeat(" ", col))
}
