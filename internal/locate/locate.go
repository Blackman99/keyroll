// Package locate lists the places on a machine where AI provider keys are
// usually pasted: agent harness configs, app state, shell files, project .env
// files, and the backups and history those leave behind.
package locate

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Class says what rotation does with a copy.
type Class string

const (
	// Live: a tool reads the key from here. rotate writes the new key.
	Live Class = "live"
	// Residue: a backup, history line or abandoned legacy file. rotate scrubs
	// the old key and does NOT write the new one (rotation should shrink the
	// number of copies, not spread the new key into the same places).
	Residue Class = "leftover"
	// Database: a SQLite file. keyroll never edits it; it prints the command.
	Database Class = "database"
)

// Location is one file to scan.
type Location struct {
	Path   string `json:"path"`
	Tool   string `json:"tool"`
	Class  Class  `json:"class"`
	Note   string `json:"note,omitempty"`
	Manual string `json:"manual,omitempty"` // command to run instead of editing
	Stream bool   `json:"-"`                // large or binary: scan in chunks, no line numbers
}

// Env is what locate needs to know about the machine (injectable for tests).
type Env struct {
	Home   string
	GOOS   string
	Getenv func(string) string
}

// FromOS reads the real environment.
func FromOS() Env {
	h, _ := os.UserHomeDir()
	return Env{Home: h, GOOS: runtime.GOOS, Getenv: os.Getenv}
}

func (e Env) get(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

func (e Env) h(parts ...string) string { return filepath.Join(append([]string{e.Home}, parts...)...) }

func (e Env) xdgConfig() string {
	if v := e.get("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	return e.h(".config")
}

func (e Env) xdgData() string {
	if v := e.get("XDG_DATA_HOME"); v != "" {
		return v
	}
	return e.h(".local", "share")
}

// appSupport is where Electron apps (VS Code, Cursor, Windsurf, Claude
// Desktop) keep per-user files.
func (e Env) appSupport() string {
	switch e.GOOS {
	case "darwin":
		return e.h("Library", "Application Support")
	case "windows":
		if v := e.get("APPDATA"); v != "" {
			return v
		}
		return e.h("AppData", "Roaming")
	}
	return e.xdgConfig()
}

// Known returns every fixed location keyroll checks, existing or not.
func Known(e Env) []Location {
	claude := e.h(".claude")
	if v := e.get("CLAUDE_CONFIG_DIR"); v != "" {
		claude = v
	}
	codex := e.h(".codex")
	if v := e.get("CODEX_HOME"); v != "" {
		codex = v
	}
	ocData := filepath.Join(e.xdgData(), "opencode")
	ocDBs := []string{filepath.Join(ocData, "opencode*.db"), filepath.Join(ocData, "opencode*.db-wal")}
	if v := e.get("OPENCODE_DB"); v != "" { // OpenCode's own override for the database path
		ocDBs = append(ocDBs, v, v+"-wal")
	}
	ocDB := hasGlob(ocDBs[0]) || (len(ocDBs) > 2 && hasGlob(ocDBs[2]))
	cfg := e.xdgConfig()
	app := e.appSupport()

	var L []Location
	add := func(tool string, c Class, note string, paths ...string) {
		for _, p := range paths {
			L = append(L, Location{Path: p, Tool: tool, Class: c, Note: note})
		}
	}

	// Claude Code
	add("Claude Code", Live, "", e.h(".claude.json"), filepath.Join(claude, ".claude.json"),
		filepath.Join(claude, "settings.json"), filepath.Join(claude, "settings.local.json"),
		filepath.Join(claude, ".credentials.json"))
	L = append(L, Location{Path: filepath.Join(app, "Claude", "claude_desktop_config.json"), Tool: "Claude Desktop", Class: Live})

	// Codex
	add("Codex", Live, "", filepath.Join(codex, "auth.json"), filepath.Join(codex, "config.toml"), filepath.Join(codex, ".env"))

	// OpenCode: V2 imports auth.json into SQLite and never writes back to it
	// (packages/core/src/database/migration/20260805200742_import_legacy_credentials.ts).
	if ocDB {
		add("OpenCode", Residue, "legacy file: OpenCode v2 imported it into opencode.db and no longer reads it", filepath.Join(ocData, "auth.json"))
	} else {
		add("OpenCode", Live, "", filepath.Join(ocData, "auth.json"))
	}
	add("OpenCode", Live, "", filepath.Join(cfg, "opencode", "opencode.json"), filepath.Join(cfg, "opencode", "opencode.jsonc"), filepath.Join(cfg, "opencode", "config.json"))

	// Cline (VS Code, CLI and JetBrains share ~/.cline/data since the file-store migration)
	add("Cline", Live, "", e.h(".cline", "data", "secrets.json"), e.h(".cline", "data", "globalState.json"))
	// Continue, Aider, Gemini CLI, Goose, Crush, llm, aichat, Zed, Pi
	add("Continue", Live, "", e.h(".continue", "config.yaml"), e.h(".continue", "config.json"), e.h(".continue", ".env"))
	add("Aider", Live, "", e.h(".aider.conf.yml"), e.h(".aider.conf.yaml"))
	add("Gemini CLI", Live, "", e.h(".gemini", ".env"), e.h(".gemini", "settings.json"))
	add("Goose", Live, "", filepath.Join(cfg, "goose", "config.yaml"), filepath.Join(cfg, "goose", "secrets.yaml"))
	add("Crush", Live, "", filepath.Join(cfg, "crush", "crush.json"), filepath.Join(e.xdgData(), "crush", "crush.json"))
	llmDir := filepath.Join(cfg, "io.datasette.llm")
	if e.GOOS == "darwin" {
		llmDir = filepath.Join(app, "io.datasette.llm")
	}
	if v := e.get("LLM_USER_PATH"); v != "" {
		llmDir = v
	}
	add("llm", Live, "", filepath.Join(llmDir, "keys.json"))
	add("aichat", Live, "", filepath.Join(cfg, "aichat", "config.yaml"))
	add("Zed", Live, "", filepath.Join(cfg, "zed", "settings.json"))
	add("Pi", Live, "", e.h(".pi", "agent", "auth.json"), e.h(".pi", "agent", "settings.json"))

	// Editors: user settings and MCP configs (tokens pasted into env blocks)
	for _, ed := range []string{"Code", "Cursor", "Windsurf", "VSCodium"} {
		add(ed+" settings", Live, "", filepath.Join(app, ed, "User", "settings.json"), filepath.Join(app, ed, "User", "mcp.json"))
	}
	add("Cursor", Live, "", e.h(".cursor", "mcp.json"))
	add("Windsurf", Live, "", e.h(".codeium", "windsurf", "mcp_config.json"))

	// Shell startup files
	for _, f := range []string{".zshrc", ".zshenv", ".zprofile", ".bashrc", ".bash_profile", ".profile", ".envrc", ".env", ".secrets", ".netrc"} {
		add("shell", Live, "", e.h(f))
	}
	add("shell", Live, "", filepath.Join(cfg, "fish", "config.fish"), filepath.Join(cfg, "fish", "fish_variables"))
	add("PowerShell", Live, "", e.h("Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		e.h("Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(cfg, "powershell", "Microsoft.PowerShell_profile.ps1"))

	// Shell history: an `export KEY=…` you typed once lives here forever.
	hist := []string{e.h(".zsh_history"), e.h(".bash_history"), e.h(".python_history"), e.h(".node_repl_history"),
		filepath.Join(e.xdgData(), "fish", "fish_history"),
		filepath.Join(e.xdgData(), "powershell", "PSReadLine", "ConsoleHost_history.txt")}
	if e.GOOS == "windows" {
		hist = append(hist, filepath.Join(app, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt"))
	}
	if v := e.get("HISTFILE"); v != "" {
		hist = append(hist, v)
	}
	for _, p := range hist {
		L = append(L, Location{Path: p, Tool: "shell history", Class: Residue, Stream: true})
	}

	// Backups the tools make for you.
	for _, p := range globAll(filepath.Join(claude, "backups", ".claude.json*"), e.h(".claude.json.backup*"), e.h(".claude.json.*.bak")) {
		L = append(L, Location{Path: p, Tool: "Claude Code", Class: Residue, Note: "automatic backup of ~/.claude.json"})
	}

	// SQLite stores: scanned as bytes, never edited.
	for _, p := range globAll(ocDBs...) {
		L = append(L, Location{Path: p, Tool: "OpenCode", Class: Database, Stream: true,
			Note: "OpenCode v2 credential store", Manual: "opencode auth login <provider>  (then: opencode auth list)"})
	}
	for _, ed := range []string{"Cursor", "Windsurf", "Code"} {
		for _, p := range globAll(filepath.Join(app, ed, "User", "globalStorage", "state.vscdb*")) {
			c := Database
			note := ed + " app state (BYOK keys, extension secrets in plain storage)"
			if strings.HasSuffix(p, ".backup") {
				note = ed + " app state backup"
			}
			L = append(L, Location{Path: p, Tool: ed, Class: c, Stream: true, Note: note,
				Manual: "change the key in " + ed + " → Settings → Models (API keys), then restart " + ed})
		}
	}

	// keyroll's own backups hold the old key until you delete them; count them.
	for _, p := range walkFiles(e.h(".keyroll", "backups")) {
		L = append(L, Location{Path: p, Tool: "keyroll backup", Class: Residue, Note: "keyroll's own backup: delete it once the old key is revoked"})
	}

	// Backup siblings of every live text file: foo.bak, foo~, foo.orig, foo.old, foo.backup*, foo.save
	var extra []Location
	for _, l := range L {
		if l.Class != Live {
			continue
		}
		for _, b := range siblings(l.Path) {
			extra = append(extra, Location{Path: b, Tool: l.Tool, Class: Residue, Note: "backup copy of " + filepath.Base(l.Path)})
		}
	}
	return append(L, extra...)
}

var backupSuffixes = []string{".bak", "~", ".orig", ".old", ".save", ".backup", ".swp.txt"}

// IsBackupName reports whether name looks like a backup of base.
func IsBackupName(base, name string) bool {
	if name == base || !strings.HasPrefix(name, base) {
		return false
	}
	rest := name[len(base):]
	for _, s := range backupSuffixes {
		if rest == s || strings.HasPrefix(rest, s+".") || strings.HasPrefix(rest, s+"-") {
			return true
		}
	}
	// foo.backup.1760650000, foo.20261017, foo-2026-10-17
	if strings.HasPrefix(rest, ".backup") || strings.HasPrefix(rest, ".bak") {
		return true
	}
	return false
}

func siblings(path string) []string {
	dir, base := filepath.Split(path)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, en := range ents {
		if !en.IsDir() && IsBackupName(base, en.Name()) {
			out = append(out, filepath.Join(dir, en.Name()))
		}
	}
	return out
}

func walkFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() != "MANIFEST.txt" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func hasGlob(pattern string) bool {
	m, _ := filepath.Glob(pattern)
	for _, p := range m {
		if !strings.HasSuffix(p, "-wal") && !strings.HasSuffix(p, "-shm") {
			return true
		}
	}
	return false
}

func globAll(patterns ...string) []string {
	var out []string
	for _, p := range patterns {
		m, _ := filepath.Glob(p)
		for _, f := range m {
			if strings.HasSuffix(f, "-shm") {
				continue
			}
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// SkipDirs are never descended into by Walk. Agent transcripts are skipped on
// purpose: that is spillscan's job (github.com/Blackman99/spillscan).
var SkipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".hg": true, ".svn": true, "Library": true, ".cache": true,
	".npm": true, ".pnpm-store": true, ".yarn": true, ".bun": true, ".cargo": true, ".rustup": true,
	".gradle": true, ".m2": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, ".tox": true, ".Trash": true, "Trash": true, "snap": true, ".docker": true,
	"Applications": true, "Pictures": true, "Music": true, "Movies": true, "Videos": true,
	".vscode-server": true, "extensions": true,
	".local": true, ".config": true, "AppData": true, "target": true, ".next": true, ".terraform": true,
	".keyroll": true,
}

// HomeSkips are home-level directories Walk leaves alone: their files are in
// Known, and their transcripts (~/.claude/projects, ~/.codex/sessions) are
// spillscan's job.
func HomeSkips(e Env) map[string]bool {
	m := map[string]bool{e.h(".claude"): true, e.h(".codex"): true, e.h(".cline"): true, e.h(".continue"): true,
		e.h(".gemini"): true, e.h(".cursor"): true, e.h(".codeium"): true, e.h(".pi"): true,
		e.h("go", "pkg"): true} // module cache; ~/go/src projects are still walked
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if v := e.get(k); v != "" {
			m[filepath.Clean(v)] = true
		}
	}
	return m
}

// Walk finds project-level files under roots: .env files, .dev.vars,
// opencode.json, .aider.conf.yml, MCP configs, and editor Local History copies.
func Walk(roots []string, skip map[string]bool, maxDepth int, maxDirs int) (locs []Location, visited int) {
	seen := map[string]bool{}
	for _, root := range roots {
		root = filepath.Clean(root)
		base := strings.Count(root, string(os.PathSeparator))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p != root && (SkipDirs[d.Name()] || skip[p] || strings.Count(p, string(os.PathSeparator))-base >= maxDepth) {
					return fs.SkipDir
				}
				visited++
				if maxDirs > 0 && visited > maxDirs {
					return fs.SkipAll
				}
				return nil
			}
			if l, ok := classifyProjectFile(p); ok && !seen[p] {
				seen[p] = true
				locs = append(locs, l)
			}
			return nil
		})
	}
	return locs, visited
}

func classifyProjectFile(p string) (Location, bool) {
	name := filepath.Base(p)
	parent := filepath.Base(filepath.Dir(p))
	lower := strings.ToLower(name)
	if parent == ".history" && strings.HasPrefix(lower, ".env") {
		return Location{Path: p, Tool: "editor history", Class: Residue, Note: "VS Code Local History copy"}, true
	}
	switch {
	case lower == ".env" || lower == ".dev.vars" || lower == ".envrc" || strings.HasSuffix(lower, ".env"):
		return Location{Path: p, Tool: "project .env", Class: Live}, true
	case strings.HasPrefix(lower, ".env.") || strings.HasPrefix(lower, ".dev.vars."):
		for _, t := range []string{".example", ".sample", ".template", ".dist", ".tmpl"} {
			if strings.HasSuffix(lower, t) {
				return Location{Path: p, Tool: "project .env", Class: Residue, Note: "template file: a real key should not be here"}, true
			}
		}
		for _, s := range backupSuffixes {
			if strings.Contains(lower, s) && s != "~" {
				return Location{Path: p, Tool: "project .env", Class: Residue, Note: "backup copy"}, true
			}
		}
		if strings.HasSuffix(lower, "~") {
			return Location{Path: p, Tool: "project .env", Class: Residue, Note: "backup copy"}, true
		}
		return Location{Path: p, Tool: "project .env", Class: Live}, true
	case lower == "opencode.json" || lower == "opencode.jsonc":
		return Location{Path: p, Tool: "OpenCode project", Class: Live}, true
	case lower == ".aider.conf.yml" || lower == ".aider.conf.yaml":
		return Location{Path: p, Tool: "Aider project", Class: Live}, true
	case lower == ".mcp.json":
		return Location{Path: p, Tool: "Claude Code project", Class: Live}, true
	case lower == "settings.local.json" && parent == ".claude":
		return Location{Path: p, Tool: "Claude Code project", Class: Live}, true
	case lower == "mcp.json" && (parent == ".cursor" || parent == ".vscode"):
		return Location{Path: p, Tool: "editor MCP config", Class: Live}, true
	case lower == "config.toml" && parent == ".codex":
		return Location{Path: p, Tool: "Codex project", Class: Live}, true
	}
	return Location{}, false
}
