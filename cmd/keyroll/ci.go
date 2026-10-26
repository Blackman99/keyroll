package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/Blackman99/keyroll/internal/ci"
	"github.com/Blackman99/keyroll/internal/provider"
	"github.com/Blackman99/keyroll/internal/render"
	"github.com/Blackman99/keyroll/internal/scan"
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
var ownerName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// normRepo accepts owner/name and the URLs people paste: https://github.com/o/r(.git), git@github.com:o/r.git.
func normRepo(r string) (string, bool) {
	r = strings.TrimSpace(r)
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/", "git@github.com:"} {
		r = strings.TrimPrefix(r, p)
	}
	r = strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git")
	return r, repoName.MatchString(r)
}

func (a *app) ciOptions(cmd string, repos []string) ([]string, bool) {
	var out []string
	for _, r := range repos {
		n, ok := normRepo(r)
		if !ok {
			fmt.Fprintf(a.stderr, "keyroll %s: %q is not a GitHub repository: give it as owner/name, e.g. --%s octo-org/agent-bot\n", cmd, r, map[bool]string{true: "ci", false: "repo"}[cmd == "rotate"])
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// ciError explains a lookup that could not start, and picks its exit code.
func (a *app) ciError(cmd string, err error) int {
	fmt.Fprintf(a.stderr, "keyroll %s: %v\n", cmd, err)
	if errors.Is(err, ci.ErrNoRepo) {
		return exitUsage
	}
	return exitRuntime
}

func (a *app) ciCmd(o *opts) int {
	repos, ok := a.ciOptions("ci", o.repos)
	if !ok {
		return exitUsage
	}
	// `keyroll ci owner/name` and `keyroll ci owner` read as --repo and --owner
	owners := append([]string(nil), o.owners...)
	for _, x := range o.args {
		if n, ok := normRepo(x); ok {
			repos = append(repos, n)
		} else if ownerName.MatchString(x) {
			owners = append(owners, x)
		} else {
			fmt.Fprintf(a.stderr, "keyroll ci: unexpected %q: pass a repository as owner/name or an account as --owner LOGIN\n", x)
			return exitUsage
		}
	}
	for _, x := range owners {
		if !ownerName.MatchString(x) {
			fmt.Fprintf(a.stderr, "keyroll ci: --owner %q is not a GitHub user or organization name (for one repository use --repo owner/name)\n", x)
			return exitUsage
		}
	}
	if o.limit < 1 || o.limit > 1000 {
		fmt.Fprintln(a.stderr, "keyroll ci: --limit must be between 1 and 1000 (repos per account)")
		return exitUsage
	}
	// a whole account takes a while (a few gh calls per repo): say so on a terminal
	busy := (o.all || len(owners) > 0) && isTerminalW(a.stderr)
	if busy {
		fmt.Fprint(a.stderr, "keyroll ci: asking GitHub about every repo, by name only…")
	}
	res, err := ci.Lookup(context.Background(), ci.Options{Repos: repos, Owners: owners, All: o.all, Limit: o.limit,
		Archived: o.archived, Provider: o.prov, Run: a.gh, Timeout: o.timeout})
	if busy {
		fmt.Fprint(a.stderr, "\r\x1b[K")
	}
	if err != nil {
		return a.ciError("ci", err)
	}
	if o.json {
		if c := a.emit(res); c != exitOK {
			return c
		}
	} else {
		render.CI(a.stdout, render.NewStyle(a.stdout), &res, a.ciNext(res))
	}
	if len(res.Repos) > 0 && res.Readable() == 0 {
		return exitRuntime
	}
	return exitOK
}

func isTerminalW(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ciNext finds, for each issuer with matching secrets, the local key a
// rotation would start from: a quick look at the fixed locations only.
func (a *app) ciNext(res ci.Result) map[string]render.CINext {
	if len(res.Secrets) == 0 {
		return nil
	}
	r, err := scan.Run(scan.Options{Env: a.env, NoWalk: true})
	if err != nil {
		return nil
	}
	out := map[string]render.CINext{}
	for _, k := range r.Keys {
		if k.Kind != provider.KindAPI {
			continue
		}
		n := out[k.Provider]
		n.Provider = k.Provider
		n.Keys++
		n.Fingerprint = k.Fingerprint
		if n.Keys > 1 {
			n.Fingerprint = ""
		}
		out[k.Provider] = n
	}
	return out
}

// rotateCI lists the CI secrets that may hold k's issuer's key, for `rotate --ci`.
func (a *app) rotateCI(o *opts, k *scan.Key) ([]ci.Secret, int) {
	if len(o.ciRepos) == 0 {
		return nil, -1
	}
	repos, ok := a.ciOptions("rotate", o.ciRepos)
	if !ok {
		return nil, exitUsage
	}
	res, err := ci.Lookup(context.Background(), ci.Options{Repos: repos, Provider: k.Provider, Run: a.gh})
	if err != nil {
		fmt.Fprintf(a.stderr, "keyroll rotate: --ci: %v\n  nothing was changed; drop --ci to rotate without the CI secrets\n", err)
		return nil, exitRuntime
	}
	for _, r := range res.Repos {
		if r.Error != "" {
			fmt.Fprintf(a.stderr, "keyroll rotate: --ci %s: %s\n", r.Name, r.Error)
		}
	}
	if res.Readable() == 0 {
		fmt.Fprintln(a.stderr, "  nothing was changed; fix the access above or drop --ci")
		return nil, exitRuntime
	}
	return res.For(k.Provider, k.Kind), -1
}
