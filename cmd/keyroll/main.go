// Command keyroll finds every copy of your AI provider API keys, asks each
// provider which ones still work, and rotates a key everywhere at once.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/Blackman99/keyroll/internal/check"
	"github.com/Blackman99/keyroll/internal/credstore"
	"github.com/Blackman99/keyroll/internal/locate"
	"github.com/Blackman99/keyroll/internal/mcp"
	"github.com/Blackman99/keyroll/internal/provider"
	"github.com/Blackman99/keyroll/internal/render"
	"github.com/Blackman99/keyroll/internal/rotate"
	"github.com/Blackman99/keyroll/internal/scan"
)

var version = "0.2.0-dev"

// `go install …@vX.Y.Z` does not run GoReleaser's -X flag; take the module
// version Go recorded in the binary instead of claiming a dev build.
func init() {
	if !strings.HasSuffix(version, "-dev") {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(bi.Main.Version, "v") {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

// Exit codes.
const (
	exitOK      = 0
	exitAction  = 1 // check: a rejected key is still configured somewhere / rotate: a step failed
	exitUsage   = 2
	exitRuntime = 3
)

type app struct {
	stdout, stderr io.Writer
	stdin          io.Reader
	env            locate.Env
	checker        *check.Client
	tty            func() (io.ReadWriteCloser, error) // terminal for hidden input / confirmation
	now            func() time.Time
	stores         func() credstore.Result // OS credential-store names (nil = do not ask)
}

func main() {
	a := &app{stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin, env: locate.FromOS(),
		checker: check.New(version), now: time.Now,
		tty: func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) },
		stores: func() credstore.Result {
			return credstore.Lookup(context.Background(), credstore.Env{GOOS: runtime.GOOS, Getenv: os.Getenv})
		}}
	os.Exit(a.run(os.Args[1:]))
}

const usage = `keyroll — find every copy of your AI API keys, check which still work, rotate them all at once

usage
  keyroll [map]              where every key sits: agent configs, app state, shell files, .env, backups, history,
                             plus credential-store entries AI tools keep keys in (by name only)
  keyroll check [fp…]        ask each issuer: ALIVE (accepted) or REJECTED (revoked / invalid), one free GET per key
  keyroll rotate <fp>        paste the new key once: live copies rewritten, leftovers scrubbed
  keyroll paths              every place keyroll looks, and which exist here
  keyroll mcp                read-only MCP server (stdio): fingerprints only, offline
  keyroll version

flags (map, check, rotate)
  --json                     machine-readable output (never contains key values)
  --root DIR                 walk DIR for .env and project configs (repeatable; default: your home)
  --depth N                  how deep to walk (default 6)
  --no-walk                  only the fixed harness locations, no .env walk
  --provider ID              only keys of one issuer (anthropic, openai, openrouter, google, …)
  --file PATH                also scan this file (repeatable)
  --timeout 10s              per-request limit for check and for rotate's new-key check
  --no-credstore             do not ask the OS credential store (Keychain, Credential Manager, Secret Service)

rotate
  --dry-run                  show the plan, change nothing
  --yes                      do not ask for confirmation
  --offline                  do not check that the new key works before writing it
  --no-backup                do not keep originals in ~/.keyroll/backups
  the new key is read from stdin if piped (pbpaste | keyroll rotate a91f3c2e), else asked for with echo off

exit codes: 0 ok · 1 action needed (a REJECTED key is still configured / a rotate step failed) · 2 usage · 3 error
values are never printed, logged or written to JSON: keys show as their issuer prefix + last 4 and an 8-hex fingerprint.
credential stores are asked for entry names only (security find-generic-password without -w, cmdkey /list,
Secret Service SearchItems): keyroll never reads those values, so it lists them as by-hand steps.
`

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

type opts struct {
	json, noWalk, dryRun, yes, offline, noBackup bool
	noCredstore                                  bool
	cmd                                          string
	roots, files                                 multi
	depth                                        int
	prov                                         string
	timeout                                      time.Duration
	args                                         []string
}

var knownFlags = []string{"json", "root", "depth", "no-walk", "provider", "file", "dry-run", "yes", "offline", "no-backup", "timeout", "no-credstore", "help"}

func (a *app) parse(cmd string, argv []string) (*opts, int) {
	o := &opts{cmd: cmd}
	fs := flag.NewFlagSet("keyroll "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.json, "json", false, "")
	fs.Var(&o.roots, "root", "")
	fs.IntVar(&o.depth, "depth", 6, "")
	fs.BoolVar(&o.noWalk, "no-walk", false, "")
	fs.StringVar(&o.prov, "provider", "", "")
	fs.Var(&o.files, "file", "")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Second, "")
	fs.BoolVar(&o.noCredstore, "no-credstore", false, "")
	if cmd == "rotate" {
		fs.BoolVar(&o.dryRun, "dry-run", false, "")
		fs.BoolVar(&o.yes, "yes", false, "")
		fs.BoolVar(&o.offline, "offline", false, "")
		fs.BoolVar(&o.noBackup, "no-backup", false, "")
	}
	rest := argv
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(a.stdout, usage)
				return nil, exitOK
			}
			msg := err.Error()
			if strings.HasPrefix(msg, "flag provided but not defined: ") {
				bad := strings.TrimLeft(strings.TrimPrefix(msg, "flag provided but not defined: "), "-")
				msg = "unknown option --" + bad
				if s := suggest(bad); s != "" {
					msg += " (did you mean --" + s + "?)"
				}
			}
			fmt.Fprintf(a.stderr, "keyroll %s: %s\nrun `keyroll --help` for usage\n", cmd, msg)
			return nil, exitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		o.args = append(o.args, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if o.depth < 1 || o.depth > 30 {
		fmt.Fprintf(a.stderr, "keyroll %s: --depth must be between 1 and 30\n", cmd)
		return nil, exitUsage
	}
	if o.prov != "" && provider.ByID(o.prov) == nil {
		var ids []string
		for _, p := range provider.All {
			ids = append(ids, p.ID)
		}
		fmt.Fprintf(a.stderr, "keyroll %s: unknown provider %q — one of: %s\n", cmd, o.prov, strings.Join(ids, ", "))
		return nil, exitUsage
	}
	for _, r := range o.roots {
		if st, err := os.Stat(r); err != nil {
			fmt.Fprintf(a.stderr, "keyroll %s: --root %s does not exist (pass a folder to walk, e.g. --root ~/code)\n", cmd, r)
			return nil, exitUsage
		} else if !st.IsDir() {
			fmt.Fprintf(a.stderr, "keyroll %s: --root %s is a file, not a folder: to scan one file use --file %s\n", cmd, r, r)
			return nil, exitUsage
		}
	}
	for _, f := range o.files {
		if st, err := os.Stat(f); err != nil || st.IsDir() {
			fmt.Fprintf(a.stderr, "keyroll %s: --file %s is not a readable file (for a folder use --root)\n", cmd, f)
			return nil, exitUsage
		}
	}
	return o, -1
}

func suggest(bad string) string {
	best, bd := "", 3
	for _, k := range knownFlags {
		if d := lev(bad, k); d < bd {
			best, bd = k, d
		}
	}
	return best
}

func lev(a, b string) int {
	d := make([]int, len(b)+1)
	for j := range d {
		d[j] = j
	}
	for i := 1; i <= len(a); i++ {
		prev := d[0]
		d[0] = i
		for j := 1; j <= len(b); j++ {
			tmp := d[j]
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			d[j] = min(d[j]+1, d[j-1]+1, prev+c)
			prev = tmp
		}
	}
	return d[len(b)]
}

func (a *app) run(argv []string) int {
	cmd := "map"
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		cmd, argv = argv[0], argv[1:]
	}
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, usage)
		return exitOK
	case "version", "--version":
		fmt.Fprintf(a.stdout, "keyroll %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return exitOK
	case "mcp":
		err := mcp.Run(a.stdin, a.stdout, version, func() (*scan.Report, error) {
			return scan.Run(scan.Options{Env: a.env, Stores: a.stores})
		})
		if err != nil {
			fmt.Fprintln(a.stderr, "keyroll mcp:", err)
			return exitRuntime
		}
		return exitOK
	case "paths":
		return a.paths(argv)
	case "map", "check", "rotate":
	default:
		msg := fmt.Sprintf("keyroll: unknown command %q", cmd)
		for _, c := range []string{"map", "check", "rotate", "paths", "mcp", "version"} {
			if lev(cmd, c) <= 2 {
				msg += fmt.Sprintf(" (did you mean `keyroll %s`?)", c)
				break
			}
		}
		fmt.Fprintln(a.stderr, msg+"\nrun `keyroll --help` for usage")
		return exitUsage
	}
	o, code := a.parse(cmd, argv)
	if o == nil {
		return code
	}
	switch cmd {
	case "map":
		if len(o.args) > 0 {
			fmt.Fprintf(a.stderr, "keyroll: unexpected %q — to scan a folder use --root %s\n", o.args[0], o.args[0])
			return exitUsage
		}
		return a.mapCmd(o)
	case "check":
		return a.checkCmd(o)
	default:
		return a.rotateCmd(o)
	}
}

func (a *app) scan(o *opts) (*scan.Report, error) {
	so := scan.Options{Env: a.env, Roots: o.roots, Depth: o.depth, NoWalk: o.noWalk, Provider: o.prov, Extra: o.files}
	// check has nothing to ask about a value keyroll never read: only map and rotate list store entries
	if o.cmd != "check" && !o.noCredstore {
		so.Stores = a.stores
	}
	return scan.Run(so)
}

func spillscanOnPath() bool { _, err := exec.LookPath("spillscan"); return err == nil }

func (a *app) mapCmd(o *opts) int {
	r, err := a.scan(o)
	if err != nil {
		fmt.Fprintln(a.stderr, "keyroll:", err)
		return exitRuntime
	}
	if o.json {
		return a.emit(r)
	}
	render.Map(a.stdout, render.NewStyle(a.stdout), r, spillscanOnPath())
	return exitOK
}

func (a *app) emit(v any) int {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(a.stderr, "keyroll:", err)
		return exitRuntime
	}
	return exitOK
}

func (a *app) selectKeys(r *scan.Report, qs []string) ([]*scan.Key, int) {
	if len(qs) == 0 {
		return r.Keys, -1
	}
	var out []*scan.Key
	for _, q := range qs {
		ks := r.Find(q)
		switch len(ks) {
		case 0:
			fmt.Fprintf(a.stderr, "keyroll: no key matches %q — run `keyroll` to see fingerprints\n", q)
			return nil, exitUsage
		case 1:
			out = append(out, ks[0])
		default:
			fmt.Fprintf(a.stderr, "keyroll: %q matches %d keys — use more of the fingerprint\n", q, len(ks))
			return nil, exitUsage
		}
	}
	return out, -1
}

type checkJSON struct {
	*scan.Key
	Check check.Result `json:"check"`
}

func (a *app) checkCmd(o *opts) int {
	r, err := a.scan(o)
	if err != nil {
		fmt.Fprintln(a.stderr, "keyroll:", err)
		return exitRuntime
	}
	keys, code := a.selectKeys(r, o.args)
	if keys == nil && code >= 0 {
		return code
	}
	if len(keys) == 0 {
		if o.json {
			return a.emit([]checkJSON{})
		}
		fmt.Fprintln(a.stdout, "keyroll check: no AI provider keys found, nothing to ask")
		return exitOK
	}
	jobs := make([]check.Job, len(keys))
	for i, k := range keys {
		jobs[i] = check.Job{Provider: k.Prov(), Kind: k.Kind, Secret: k.Secret()}
	}
	a.checker.HTTP.Timeout = o.timeout
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	res := a.checker.All(ctx, jobs)
	stale := 0
	for i, k := range keys {
		if res[i].State == check.Rejected && k.Count(locate.Live)+k.Count(locate.Database) > 0 {
			stale++
		}
	}
	if o.json {
		out := make([]checkJSON, len(keys))
		for i, k := range keys {
			out[i] = checkJSON{k, res[i]}
		}
		if c := a.emit(out); c != exitOK {
			return c
		}
	} else {
		rows := make([]render.CheckRow, len(keys))
		for i, k := range keys {
			rows[i] = render.CheckRow{Key: k, Result: res[i]}
		}
		render.Check(a.stdout, render.NewStyle(a.stdout), rows)
	}
	if stale > 0 {
		return exitAction
	}
	return exitOK
}

func (a *app) rotateCmd(o *opts) int {
	if len(o.args) != 1 {
		fmt.Fprintln(a.stderr, "keyroll rotate: give exactly one fingerprint, e.g. `keyroll rotate a91f3c2e` (run `keyroll` to see them)")
		return exitUsage
	}
	r, err := a.scan(o)
	if err != nil {
		fmt.Fprintln(a.stderr, "keyroll:", err)
		return exitRuntime
	}
	keys, code := a.selectKeys(r, o.args)
	if keys == nil {
		return code
	}
	k := keys[0]
	pl, err := rotate.MakePlan(k)
	if err != nil {
		fmt.Fprintf(a.stderr, "keyroll rotate: %s %s: %v\n", k.ProviderNam, k.Hint, err)
		return exitUsage
	}
	pl.Stores = r.CredentialStore.For(k.Provider)
	st := render.NewStyle(a.stdout)
	if o.dryRun {
		if o.json {
			return a.emit(pl)
		}
		render.Plan(a.stdout, st, pl, "key (not asked for in a dry run)", "")
		w, s, m := pl.Counts()
		fmt.Fprintf(a.stdout, "  dry run: would rewrite %d, scrub %d, leave %d for you%s. nothing changed.\n", w, s, m, storeNote(pl))
		return exitOK
	}
	newKey, err := a.readNewKey(k)
	if err != nil {
		fmt.Fprintln(a.stderr, "keyroll rotate:", err)
		return exitUsage
	}
	newKey, err = rotate.ValidateNew(k, newKey)
	if err != nil {
		fmt.Fprintln(a.stderr, "keyroll rotate:", err)
		return exitUsage
	}
	state := ""
	if !o.offline && check.Skip(k.Prov(), provider.KindAPI) == "" {
		a.checker.HTTP.Timeout = o.timeout
		res := a.checker.One(context.Background(), k.Prov(), provider.KindAPI, newKey)
		switch res.State {
		case check.Rejected:
			fmt.Fprintf(a.stderr, "keyroll rotate: %s says the new key does not work: %s. Nothing was written. Copy it again from %s\n", k.ProviderNam, res.Detail, k.Prov().KeysURL)
			return exitUsage
		case check.Alive:
			state = st.Green + "(works ✓)" + st.Reset
		default:
			state = st.Yellow + "(could not confirm: " + res.Detail + ")" + st.Reset
		}
	}
	if !o.json {
		render.Plan(a.stdout, st, pl, provider.Hint(newKey), state)
	}
	w, s, m := pl.Counts()
	if w+s == 0 {
		fmt.Fprintf(a.stdout, "  nothing keyroll can edit: %d place(s)%s need the steps above.\n", m, storeNote(pl))
		return exitOK
	}
	if !o.yes {
		ok, err := a.confirm(fmt.Sprintf("  rewrite %d, scrub %d%s%s? [y/N] ", w, s, map[bool]string{true: fmt.Sprintf(", %d by hand", m), false: ""}[m > 0], storeNote(pl)))
		if err != nil {
			fmt.Fprintln(a.stderr, "keyroll rotate:", err)
			return exitUsage
		}
		if !ok {
			fmt.Fprintln(a.stdout, "  nothing changed.")
			return exitOK
		}
	}
	res, err := rotate.Apply(pl, newKey, rotate.Options{BackupRoot: filepath.Join(a.env.Home, ".keyroll", "backups"), Home: a.env.Home, NoBackup: o.noBackup, Now: a.now()})
	if err != nil {
		fmt.Fprintln(a.stderr, "keyroll rotate:", err)
		return exitRuntime
	}
	// Every rotation ends with the question that matters: is the OLD key revoked?
	old := check.Result{State: check.Skipped, Detail: "--offline"}
	if !o.offline {
		if why := check.Skip(k.Prov(), k.Kind); why != "" {
			old.Detail = why
		} else {
			a.checker.HTTP.Timeout = o.timeout
			old = a.checker.One(context.Background(), k.Prov(), k.Kind, k.Secret())
		}
	}
	if o.json {
		a.emit(struct {
			rotate.Result
			OldKey check.Result `json:"old_key"`
		}{res, old})
	} else {
		render.Applied(a.stdout, st, res, a.env.Home, old.State)
		if after, err := a.scan(o); err == nil {
			left, where := 0, []string{}
			for _, ak := range after.Find(k.Fingerprint) {
				for _, c := range ak.Copies {
					if c.Tool != "keyroll backup" {
						left++
						where = append(where, c.Tool+" "+render.ClassLabel(c.Class))
					}
				}
			}
			msg := fmt.Sprintf("\n  re-scanned: the old key %s was in %d places, now %d", k.Hint, len(k.Copies), left)
			if left > 0 {
				msg += " (" + strings.Join(where, ", ") + ": see step 1)"
			}
			fmt.Fprintln(a.stdout, msg)
		}
		render.OldKey(a.stdout, st, k, old, res.BackupDir != "")
	}
	for _, s := range res.Plan.Steps {
		if s.Error != "" {
			return exitAction
		}
	}
	return exitOK
}

// storeNote counts the credential-store entries a plan asks the user to check.
func storeNote(pl rotate.Plan) string {
	switch n := len(pl.Stores); n {
	case 0:
		return ""
	case 1:
		return " + 1 credential-store entry to check"
	default:
		return fmt.Sprintf(" + %d credential-store entries to check", n)
	}
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (a *app) readNewKey(k *scan.Key) (string, error) {
	if !isTerminal(a.stdin) {
		b, err := io.ReadAll(io.LimitReader(a.stdin, 4096))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(b)) == "" {
			return "", errors.New("stdin was empty: pipe the new key in (pbpaste | keyroll rotate " + k.Fingerprint + ") or run it in a terminal")
		}
		return string(b), nil
	}
	tty, err := a.tty()
	if err != nil {
		return "", errors.New("no terminal to ask on: pipe the new key in, e.g. `pbpaste | keyroll rotate " + k.Fingerprint + " --yes`")
	}
	defer tty.Close()
	fmt.Fprintf(tty, "paste the new %s key (input hidden), then Enter: ", k.ProviderNam)
	restore := noEcho(tty)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		if _, ok := <-sig; ok {
			restore()
			fmt.Fprintln(tty)
			os.Exit(130)
		}
	}()
	line, err := bufio.NewReader(tty).ReadString('\n')
	restore()
	signal.Stop(sig)
	close(sig)
	fmt.Fprintln(tty)
	if err != nil && line == "" {
		return "", errors.New("no key entered")
	}
	return line, nil
}

func noEcho(tty io.ReadWriteCloser) func() {
	f, ok := tty.(*os.File)
	if !ok || runtime.GOOS == "windows" {
		return func() {}
	}
	off := exec.Command("stty", "-echo")
	off.Stdin = f
	if off.Run() != nil {
		return func() {}
	}
	done := false
	return func() {
		if done {
			return
		}
		done = true
		on := exec.Command("stty", "echo")
		on.Stdin = f
		_ = on.Run()
	}
}

func (a *app) confirm(prompt string) (bool, error) {
	var rw io.ReadWriter
	if isTerminal(a.stdin) {
		rw = struct {
			io.Reader
			io.Writer
		}{a.stdin, a.stdout}
	} else {
		t, err := a.tty()
		if err != nil {
			return false, errors.New("cannot ask for confirmation without a terminal: add --yes")
		}
		defer t.Close()
		rw = t
	}
	fmt.Fprint(rw, prompt)
	line, _ := bufio.NewReader(rw).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

func (a *app) paths(argv []string) int {
	asJSON := false
	for _, x := range argv {
		switch x {
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(a.stderr, "keyroll paths: unexpected %q\n", x)
			return exitUsage
		}
	}
	locs := locate.Known(a.env)
	sort.SliceStable(locs, func(i, j int) bool { return locs[i].Tool < locs[j].Tool })
	type row struct {
		locate.Location
		Exists bool `json:"exists"`
	}
	var rows []row
	for _, l := range locs {
		_, err := os.Stat(l.Path)
		rows = append(rows, row{l, err == nil})
	}
	if asJSON {
		return a.emit(rows)
	}
	st := render.NewStyle(a.stdout)
	n := 0
	for _, r := range rows {
		mark := st.Dim + "·" + st.Reset
		if r.Exists {
			mark = st.Green + "●" + st.Reset
			n++
		}
		p := r.Path
		if rel, err := filepath.Rel(a.env.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = filepath.ToSlash(filepath.Join("~", rel))
		}
		fmt.Fprintf(a.stdout, "  %s %-17s %-9s %s\n", mark, r.Tool, render.ClassLabel(r.Class), p)
	}
	fmt.Fprintf(a.stdout, "\n  %d of %d fixed locations exist here (● = exists). keyroll also walks your home for .env, .dev.vars, .envrc,\n  opencode.json, .aider.conf.yml, .mcp.json, .claude/settings.local.json and editor Local History copies.\n  session transcripts are left to spillscan.\n", n, len(rows))
	if store, names := credstore.Queries(credstore.Env{GOOS: a.env.GOOS, Getenv: a.env.Getenv}); store != "" {
		fmt.Fprintf(a.stdout, "\n  %s: keyroll asks whether these entries exist, by name only (it never reads a value):\n", store)
		for _, nm := range names {
			fmt.Fprintf(a.stdout, "    %s\n", nm)
		}
	}
	return exitOK
}
