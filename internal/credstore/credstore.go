// Package credstore lists OS credential-store entries that AI tools are known
// to keep provider keys in: the macOS Keychain, Windows Credential Manager and
// the freedesktop Secret Service (GNOME Keyring, KWallet) on Linux.
//
// Names only, never values. keyroll asks each store whether an entry with a
// known name exists and reads its public attributes (service, account,
// modification date). It never asks for, decrypts or prints a secret:
//
//   - macOS:   security find-generic-password -s NAME   (no -w, no -g)
//     security find-internet-password -s URL           (no -w, no -g)
//   - Windows: cmdkey /list                              (lists targets; it cannot show passwords)
//   - Linux:   gdbus call … Secret.Service.SearchItems   (returns item paths; GetSecrets is never called)
//
// Because the value is never read, an entry has no fingerprint: keyroll cannot
// tell which key it holds. It is shown as a "by hand" reminder with the exact
// step that replaces it, for the issuer the entry belongs to.
package credstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Holds says what kind of credential an entry keeps.
type Holds string

const (
	APIKey      Holds = "api key"          // one issuer's API key: replace it when you rotate that key
	APIKeys     Holds = "api keys"         // several issuers' keys in one entry (Goose keeps a JSON blob)
	KeyOrLogin  Holds = "api key or login" // Codex: an API key or a ChatGPT login, depending on how you logged in
	LoginTokens Holds = "login"            // OAuth login the tool refreshes itself: log out and in again if it leaked
)

// Entry is one credential-store item keyroll found. It never carries a value.
type Entry struct {
	Store    string `json:"store"`              // "macOS Keychain", "Windows Credential Manager", "Secret Service"
	Tool     string `json:"tool"`               // "Claude Code", "Zed", …
	Name     string `json:"name"`               // service name, server URL or target, as stored
	Account  string `json:"account,omitempty"`  // the item's account attribute (a user or slot name, not a secret)
	Provider string `json:"provider,omitempty"` // issuer id when the entry belongs to one issuer
	Holds    Holds  `json:"holds"`
	Modified string `json:"modified,omitempty"` // YYYY-MM-DD, when the store reports it (macOS)
	Locked   bool   `json:"locked,omitempty"`   // Secret Service: found in a locked collection
	Manual   string `json:"manual"`             // how to replace it by hand
	Command  string `json:"command"`            // the read-only query that found it (re-run it to confirm by hand)
}

// Result is one lookup.
type Result struct {
	Store   string  `json:"store,omitempty"`
	Asked   int     `json:"asked"` // how many known entry names keyroll looked for
	Entries []Entry `json:"entries"`
	Skipped string  `json:"skipped,omitempty"` // why the store was not asked, if it was not
}

// For returns the entries that may hold a key of providerID: the ones that
// belong to that issuer and the multi-issuer ones. Login entries are left out:
// rotating an API key does not touch them.
func (r *Result) For(providerID string) []Entry {
	if r == nil {
		return nil
	}
	var out []Entry
	for _, e := range r.Entries {
		if e.Holds == LoginTokens {
			continue
		}
		if e.Provider == providerID || e.Provider == "" {
			out = append(out, e)
		}
	}
	return out
}

// Filter keeps the entries of one issuer (plus multi-issuer entries).
func (r Result) Filter(providerID string) Result {
	if providerID == "" {
		return r
	}
	keep := []Entry{}
	for _, e := range r.Entries {
		if e.Provider == providerID || e.Provider == "" {
			keep = append(keep, e)
		}
	}
	r.Entries = keep
	return r
}

// Runner runs a command and returns its standard output and exit code. err is
// set only when the command could not run at all (not installed, timed out).
type Runner func(ctx context.Context, name string, args ...string) (stdout []byte, exit int, err error)

// Display is the entry name as keyroll prints it: service and target names
// quoted ("Claude Code"), URLs and attribute lists as they are.
func (e Entry) Display() string {
	if strings.Contains(e.Name, "://") || strings.Contains(e.Name, "=") {
		return e.Name
	}
	return `"` + e.Name + `"`
}

// Exec is the real Runner. Standard error is discarded.
func Exec(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	if err := safe(name, args); err != nil {
		return nil, -1, err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, -1, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), ee.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, err
	}
	return out.Bytes(), 0, nil
}

// errUnsafe is returned for any command that could reveal a secret value.
var errUnsafe = errors.New("credstore: refusing a command that could print a secret value")

// safe is the second line of defence behind the fixed command list below: the
// only commands keyroll ever runs against a credential store are attribute and
// name queries.
func safe(name string, args []string) error {
	switch name {
	case "security":
		if len(args) == 0 || (args[0] != "find-generic-password" && args[0] != "find-internet-password") {
			return errUnsafe
		}
		for _, a := range args[1:] {
			if a == "-w" || a == "-g" || strings.HasPrefix(a, "-w") || strings.HasPrefix(a, "-g") {
				return errUnsafe
			}
		}
	case "cmdkey":
		if len(args) != 1 || args[0] != "/list" {
			return errUnsafe
		}
	case "gdbus":
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "org.freedesktop.Secret.Service.SearchItems") || strings.Contains(joined, "GetSecret") {
			return errUnsafe
		}
	default:
		return errUnsafe
	}
	return nil
}

// Env is what Lookup needs (injectable for tests).
type Env struct {
	GOOS    string
	Getenv  func(string) string
	Run     Runner
	Timeout time.Duration // per command; default 4s
}

func (e Env) get(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// Zed keeps each provider's key under that provider's API URL
// (crates/language_model/src/api_key.rs: write_credentials(url, "Bearer", key)).
// These are Zed's default URLs; a custom api_url in Zed's settings is stored
// under that URL instead (Windows and Linux list those too, by host).
var zedURLs = []struct{ url, provider, name string }{
	{"https://api.anthropic.com", "anthropic", "Anthropic"},
	{"https://api.openai.com/v1", "openai", "OpenAI"},
	{"https://openrouter.ai/api/v1", "openrouter", "OpenRouter"},
	{"https://generativelanguage.googleapis.com", "google", "Google AI"},
	{"https://api.x.ai/v1", "xai", "xAI"},
	{"https://api.deepseek.com/v1", "deepseek", "DeepSeek"},
	{"https://api.mistral.ai/v1", "mistral", "Mistral"},
}

var zedHosts = map[string]string{
	"api.anthropic.com": "anthropic", "api.openai.com": "openai", "openrouter.ai": "openrouter",
	"generativelanguage.googleapis.com": "google", "api.x.ai": "xai", "api.deepseek.com": "deepseek",
	"api.mistral.ai": "mistral", "api.groq.com": "groq", "api.together.xyz": "together",
	"api.cerebras.ai": "cerebras", "api.fireworks.ai": "fireworks", "api.moonshot.ai": "moonshot",
	"api.z.ai": "zai", "api.perplexity.ai": "perplexity",
}

func zedName(provider string) string {
	for _, z := range zedURLs {
		if z.provider == provider {
			return z.name
		}
	}
	return provider
}

func zedManual(provider string) string {
	return "in Zed: `agent: open settings` → " + zedName(provider) + " → Reset Key, paste the new key"
}

func zedProvider(url string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	return zedHosts[host]
}

// Manual steps, verified against each tool's docs or source.
const (
	manClaudeKey   = "run `claude`, /logout, then /login with your Console account"
	manClaudeLogin = "only if it leaked: run `claude`, /logout, then /login"
	manCodex       = "codex login --with-api-key  (reads the new key from stdin)"
	manGeminiKey   = "run `gemini`, /auth → Use Gemini API key, paste the new key"
	manGeminiLogin = "only if it leaked: run `gemini`, /auth, sign in again"
	manGoose       = "goose configure → Configure Providers → re-enter the key"
)

type known struct {
	tool, provider string
	holds          Holds
	manual         string
}

var (
	kClaudeKey   = known{"Claude Code", "anthropic", APIKey, manClaudeKey}
	kClaudeLogin = known{"Claude Code", "anthropic", LoginTokens, manClaudeLogin}
	kCodex       = known{"Codex", "openai", KeyOrLogin, manCodex}
	kGeminiKey   = known{"Gemini CLI", "google", APIKey, manGeminiKey}
	kGeminiLogin = known{"Gemini CLI", "google", LoginTokens, manGeminiLogin}
	kGoose       = known{"Goose", "", APIKeys, manGoose}
)

// Lookup asks this machine's credential store which known entries exist.
func Lookup(ctx context.Context, e Env) Result {
	if e.Run == nil {
		e.Run = Exec
	}
	if e.Timeout == 0 {
		e.Timeout = 4 * time.Second
	}
	switch e.GOOS {
	case "darwin":
		return macOS(ctx, e)
	case "windows":
		return windows(ctx, e)
	case "linux", "freebsd", "openbsd", "netbsd":
		return secretService(ctx, e)
	}
	return Result{Skipped: "no credential store support for " + e.GOOS, Entries: []Entry{}}
}

func (e Env) run(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	if err := safe(name, args); err != nil {
		return nil, -1, err
	}
	c, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	return e.Run(c, name, args...)
}

// ---- macOS Keychain --------------------------------------------------------

type macQuery struct {
	internet bool
	name     string // service (generic) or server (internet)
	k        known
}

// claudeService is Claude Code's Keychain service name: "Claude Code" for the
// API key a Console login creates, "Claude Code-credentials" for the claude.ai
// OAuth login, each with "-" + sha256(CLAUDE_CONFIG_DIR)[:8] appended when
// CLAUDE_CONFIG_DIR is set (src/utils/secureStorage/macOsKeychainHelpers.ts).
func claudeService(suffix, configDir string) string {
	s := "Claude Code" + suffix
	if configDir != "" {
		sum := sha256.Sum256([]byte(configDir))
		s += "-" + hex.EncodeToString(sum[:])[:8]
	}
	return s
}

// MacQueries returns the names keyroll asks the Keychain about.
func macQueries(e Env) []macQuery {
	q := []macQuery{
		{name: claudeService("", ""), k: kClaudeKey},
		{name: claudeService("-credentials", ""), k: kClaudeLogin},
	}
	if d := e.get("CLAUDE_CONFIG_DIR"); d != "" {
		q = append(q, macQuery{name: claudeService("", d), k: kClaudeKey},
			macQuery{name: claudeService("-credentials", d), k: kClaudeLogin})
	}
	q = append(q,
		macQuery{name: "Codex Auth", k: kCodex},             // codex-rs/login/src/auth/storage.rs KEYRING_SERVICE
		macQuery{name: "gemini-cli-api-key", k: kGeminiKey}, // packages/core/src/core/apiKeyCredentialStorage.ts
		macQuery{name: "gemini-cli-oauth", k: kGeminiLogin}, // packages/core/src/code_assist/oauth-credential-storage.ts
		macQuery{name: "goose", k: kGoose},                  // crates/goose/src/config/base.rs KEYRING_SERVICE
	)
	for _, z := range zedURLs {
		q = append(q, macQuery{internet: true, name: z.url, k: known{"Zed", z.provider, APIKey, zedManual(z.provider)}})
	}
	return q
}

func macOS(ctx context.Context, e Env) Result {
	r := Result{Store: "macOS Keychain", Entries: []Entry{}}
	qs := macQueries(e)
	r.Asked = len(qs)
	// one `security` process per name, in parallel: ~15 short lookups
	type answer struct {
		args []string
		out  []byte
		code int
		err  error
	}
	ans := make([]answer, len(qs))
	var wg sync.WaitGroup
	for i, q := range qs {
		args := []string{"find-generic-password", "-s", q.name}
		if q.internet {
			args = []string{"find-internet-password", "-s", q.name}
		}
		wg.Add(1)
		go func(i int, args []string) {
			defer wg.Done()
			out, code, err := e.run(ctx, "security", args...)
			ans[i] = answer{args, out, code, err}
		}(i, args)
	}
	wg.Wait()
	failed := 0
	for i, q := range qs {
		a := ans[i]
		if a.err != nil {
			if errors.Is(a.err, context.DeadlineExceeded) {
				r.Skipped = "the Keychain did not answer (locked?)"
			} else {
				r.Skipped = "could not run `security`: " + a.err.Error()
			}
			r.Entries = []Entry{}
			return r
		}
		switch a.code {
		case 0:
			at := ParseSecurity(a.out)
			name := at.Service
			if q.internet {
				name = at.Server
			}
			if name == "" {
				name = q.name
			}
			r.Entries = append(r.Entries, Entry{Store: r.Store, Tool: q.k.tool, Name: name, Account: at.Account,
				Provider: q.k.provider, Holds: q.k.holds, Modified: at.Modified, Manual: q.k.manual,
				Command: "security " + a.args[0] + " -s " + shellQuote(q.name)})
		case 44: // errSecItemNotFound
		default:
			failed++
		}
	}
	if failed == len(qs) {
		r.Skipped = "`security` failed for every query (is the login keychain available?)"
	}
	return r
}

// SecurityAttrs are the public attributes keyroll reads from `security
// find-*-password` output (printed without -g, so there is no password line;
// any other line is ignored).
type SecurityAttrs struct {
	Keychain, Service, Server, Account, Modified string
}

var (
	secAttr = regexp.MustCompile(`^\s*"(svce|srvr|acct|mdat)"<[a-z0-9]+>=(.*)$`)
	secStr  = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"\s*$`)
	secDate = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})`)
)

// ParseSecurity reads `security find-generic-password -s NAME` output.
func ParseSecurity(out []byte) SecurityAttrs {
	var a SecurityAttrs
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "keychain: ") {
			a.Keychain = strings.Trim(strings.TrimPrefix(line, "keychain: "), `"`)
			continue
		}
		m := secAttr.FindStringSubmatch(line)
		if m == nil || m[2] == "<NULL>" {
			continue
		}
		s := secStr.FindStringSubmatch(m[2])
		if s == nil {
			continue
		}
		v := strings.ReplaceAll(s[1], `\000`, "")
		switch m[1] {
		case "svce":
			a.Service = v
		case "srvr":
			a.Server = v
		case "acct":
			a.Account = v
		case "mdat":
			if d := secDate.FindStringSubmatch(v); d != nil {
				a.Modified = d[1] + "-" + d[2] + "-" + d[3]
			}
		}
	}
	return a
}

func shellQuote(s string) string {
	if strings.ContainsAny(s, " '\"|&;$`\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// ---- Windows Credential Manager ---------------------------------------------

// windowsMatch maps a Credential Manager target to a known tool.
//   - keyring-rs (Codex, Goose) uses "<user>.<service>" as the target
//   - keytar (Gemini CLI) uses "<service>/<account>"
//   - Zed uses "zed:url=<api url>" (crates/gpui_windows/src/util.rs)
func windowsMatch(target string) (known, string, bool) {
	switch {
	case strings.HasSuffix(target, ".Codex Auth"):
		return kCodex, strings.TrimSuffix(target, ".Codex Auth"), true
	case target == "secrets.goose":
		return kGoose, "secrets", true
	case strings.HasPrefix(target, "gemini-cli-api-key/"):
		return kGeminiKey, strings.TrimPrefix(target, "gemini-cli-api-key/"), true
	case strings.HasPrefix(target, "gemini-cli-oauth/"):
		return kGeminiLogin, strings.TrimPrefix(target, "gemini-cli-oauth/"), true
	case strings.HasPrefix(target, "zed:url="):
		p := zedProvider(strings.TrimPrefix(target, "zed:url="))
		k := known{"Zed", p, APIKey, zedManual(p)}
		if p == "" {
			k.manual = "in Zed: `agent: open settings` → the provider with this API URL → Reset Key, paste the new key"
			k.provider = "?"
		}
		return k, "Bearer", true
	}
	return known{}, "", false
}

// windowsPatterns is how many known target patterns windowsMatch checks.
const windowsPatterns = 5

// ParseCmdkey returns the targets in `cmdkey /list` output. Each credential is
// a block whose first line is "Target: …" (the label is localised: "Ziel:",
// "Cible :"), so the parser takes the value of each block's first line and,
// when present, the part after "target=" ("LegacyGeneric:target=secrets.goose").
// cmdkey never prints a password.
func ParseCmdkey(out []byte) []string {
	out = bytes.ReplaceAll(out, []byte{0}, nil) // tolerate UTF-16 output
	var ts []string
	first := true
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			first = true
			continue
		}
		if !first {
			continue
		}
		first = false
		i := strings.Index(line, ": ")
		if i < 0 {
			continue // "* NONE *"
		}
		v := strings.TrimSpace(line[i+2:])
		if j := strings.Index(v, "target="); j >= 0 {
			v = strings.TrimSpace(v[j+len("target="):])
		}
		if v != "" {
			ts = append(ts, v)
		}
	}
	return ts
}

func windows(ctx context.Context, e Env) Result {
	r := Result{Store: "Windows Credential Manager", Asked: windowsPatterns, Entries: []Entry{}}
	out, code, err := e.run(ctx, "cmdkey", "/list")
	if err != nil {
		r.Skipped = "could not run `cmdkey /list`: " + err.Error()
		return r
	}
	if code != 0 {
		r.Skipped = fmt.Sprintf("`cmdkey /list` exited %d", code)
		return r
	}
	for _, t := range ParseCmdkey(out) {
		k, acct, ok := windowsMatch(t)
		if !ok {
			continue
		}
		prov := k.provider
		if prov == "?" {
			prov = ""
		}
		r.Entries = append(r.Entries, Entry{Store: r.Store, Tool: k.tool, Name: t, Account: acct, Provider: prov,
			Holds: k.holds, Manual: k.manual, Command: "cmdkey /list"})
	}
	return r
}

// ---- Secret Service (Linux, BSD) --------------------------------------------

type ssQuery struct {
	attrs [][2]string
	k     known
}

func ssQueries() []ssQuery {
	q := []ssQuery{
		{[][2]string{{"service", "Codex Auth"}}, kCodex},             // keyring-rs: service/username/target/application
		{[][2]string{{"service", "gemini-cli-api-key"}}, kGeminiKey}, // keytar: service/account
		{[][2]string{{"service", "gemini-cli-oauth"}}, kGeminiLogin},
		{[][2]string{{"service", "goose"}, {"username", "secrets"}}, kGoose},
	}
	for _, z := range zedURLs { // crates/gpui_linux/src/linux/platform.rs: attributes url, username
		q = append(q, ssQuery{[][2]string{{"url", z.url}}, known{"Zed", z.provider, APIKey, zedManual(z.provider)}})
	}
	return q
}

func (q ssQuery) dict() string {
	parts := make([]string, len(q.attrs))
	for i, a := range q.attrs {
		parts[i] = "'" + a[0] + "': '" + strings.ReplaceAll(a[1], "'", `\'`) + "'"
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (q ssQuery) name() string {
	parts := make([]string, len(q.attrs))
	for i, a := range q.attrs {
		parts[i] = a[0] + "=" + a[1]
	}
	return strings.Join(parts, " ")
}

var objPath = regexp.MustCompile(`'(/org/freedesktop/secrets/[^']*)'`)

// ParseSearchItems reads gdbus's answer to SearchItems: two lists of object
// paths, (unlocked, locked). It returns how many items each list holds.
func ParseSearchItems(out []byte) (unlocked, locked int, ok bool) {
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "(") {
		return 0, 0, false
	}
	// split at the comma between the two arrays: "([…], […])" or "(@ao [], @ao [])"
	depth, cut := 0, -1
	for i, c := range s {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 && cut < 0 {
				cut = i
			}
		}
	}
	if cut < 0 {
		return 0, 0, false
	}
	return len(objPath.FindAllString(s[:cut], -1)), len(objPath.FindAllString(s[cut:], -1)), true
}

func secretService(ctx context.Context, e Env) Result {
	r := Result{Store: "Secret Service", Entries: []Entry{}}
	qs := ssQueries()
	r.Asked = len(qs)
	if e.get("DBUS_SESSION_BUS_ADDRESS") == "" && e.get("XDG_RUNTIME_DIR") == "" {
		r.Skipped = "no D-Bus session (headless or SSH session)"
		return r
	}
	for i, q := range qs {
		args := []string{"call", "--session", "--timeout", "3", "--dest", "org.freedesktop.secrets",
			"--object-path", "/org/freedesktop/secrets", "--method", "org.freedesktop.Secret.Service.SearchItems", q.dict()}
		out, code, err := e.run(ctx, "gdbus", args...)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				r.Skipped = "gdbus (GLib) is not installed, so the Secret Service was not asked"
			} else {
				r.Skipped = "could not ask the Secret Service: " + err.Error()
			}
			r.Entries = []Entry{}
			return r
		}
		if code != 0 {
			if i == 0 {
				r.Skipped = "no Secret Service is running (GNOME Keyring / KWallet)"
				return r
			}
			continue
		}
		un, lo, ok := ParseSearchItems(out)
		if !ok || un+lo == 0 {
			continue
		}
		acct := ""
		for _, a := range q.attrs {
			if a[0] == "username" || a[0] == "account" {
				acct = a[1]
			}
		}
		r.Entries = append(r.Entries, Entry{Store: r.Store, Tool: q.k.tool, Name: q.name(), Account: acct,
			Provider: q.k.provider, Holds: q.k.holds, Locked: un == 0, Manual: q.k.manual,
			Command: "gdbus call --session --dest org.freedesktop.secrets --object-path /org/freedesktop/secrets --method org.freedesktop.Secret.Service.SearchItems \"" + q.dict() + "\""})
	}
	return r
}

// Canned is a Runner for tests: it answers each command line ("security
// find-generic-password -s goose") with recorded output and an exit code, and
// "not found" (exit 44 for security, empty for the rest) for everything else.
// Calls records every command it was asked to run.
type Canned struct {
	Replies map[string]Reply
	Missing error // returned for every call when set (e.g. exec.ErrNotFound)
	Calls   []string
	mu      sync.Mutex
}

// Reply is one canned answer.
type Reply struct {
	Out  []byte
	Exit int
}

// Run implements Runner.
func (c *Canned) Run(_ context.Context, name string, args ...string) ([]byte, int, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	c.mu.Lock()
	c.Calls = append(c.Calls, line)
	c.mu.Unlock()
	if err := safe(name, args); err != nil {
		return nil, -1, err
	}
	if c.Missing != nil {
		return nil, -1, c.Missing
	}
	if r, ok := c.Replies[line]; ok {
		return r.Out, r.Exit, nil
	}
	for k, r := range c.Replies { // gdbus: match on the attribute dict at the end
		if strings.HasPrefix(k, "gdbus ") && strings.HasPrefix(line, "gdbus ") && strings.HasSuffix(line, strings.TrimPrefix(k, "gdbus ")) {
			return r.Out, r.Exit, nil
		}
	}
	switch name {
	case "security":
		return nil, 44, nil
	case "gdbus":
		return []byte("(@ao [], @ao [])\n"), 0, nil
	}
	return nil, 0, nil
}

// Queries lists, without running anything, which store this OS has and the
// entry names keyroll asks it about (for `keyroll paths`).
func Queries(e Env) (store string, names []string) {
	switch e.GOOS {
	case "darwin":
		for _, q := range macQueries(e) {
			names = append(names, q.k.tool+": "+q.name)
		}
		return "macOS Keychain", names
	case "windows":
		return "Windows Credential Manager", []string{"Codex: <user>.Codex Auth", "Goose: secrets.goose",
			"Gemini CLI: gemini-cli-api-key/…", "Gemini CLI: gemini-cli-oauth/…", "Zed: zed:url=…"}
	case "linux", "freebsd", "openbsd", "netbsd":
		for _, q := range ssQueries() {
			names = append(names, q.k.tool+": "+q.name())
		}
		return "Secret Service", names
	}
	return "", nil
}
