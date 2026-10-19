# Roadmap

Every item serves [GOAL.md](GOAL.md): find every copy, tell which still work, rotate everywhere at once.

## v0.1 — shipped 2026-10-18
- Map of 15 issuers' keys across 25+ harness / app / shell locations on macOS, Linux and Windows, plus a home walk for `.env`, `.dev.vars`, `.envrc`, `opencode.json`, `.aider.conf.yml`, `.mcp.json`, Local History copies.
- live / leftover / database classes; OpenCode v2 legacy `auth.json` detection; Claude Code `~/.claude/backups`; shell history; symlinked dotfiles written through; git-tracked files flagged.
- `check` against each issuer's free metadata endpoint (answers recorded from the real APIs in `internal/check/testdata`), OpenRouter spend-cap read-out, no redirects followed, key only in headers.
- `rotate` with plan, confirmation, hidden prompt, new-key validation (shape + liveness), atomic same-mode writes, backups, re-scan.
- Read-only MCP stdio server (`key_map`, `key_copies`).

## v0.2 — in progress
- ✅ **Keychain / credential-manager awareness** (names only, never values), on `main`, not released yet: entries Claude Code, Codex, Gemini CLI, Goose and Zed keep in the macOS Keychain (`security find-*-password` without `-w`/`-g`), Windows Credential Manager (`cmdkey /list`) and the Secret Service (D-Bus `SearchItems`, never `GetSecrets`) are listed in the map and in every matching rotation plan as "by hand" steps. CI checks it against all three real stores.
- **More caps**: read per-key limits where providers expose them (Anthropic workspace limits via an admin key the user passes explicitly; OpenAI project budgets).
- **CI secrets inventory**: `keyroll ci` lists which GitHub Actions secrets *names* look like provider keys in your repos (`gh secret list`), so the "where else" list includes CI.
- `rotate --from-file` for teams that keep a new key in a password manager CLI (`op read`, `bw get`).

## v0.3
- `keyroll watch`: warn when a new copy of a known key appears (a tool wrote a backup, you pasted into a new `.env`).
- Per-tool restart hints verified per version (which tools re-read keys live and which cache them).

## v1.0
- Stable JSON schema (versioned), SARIF output for `check`, signed release binaries + SBOM.

## Will not do
- Store, sync or proxy keys. Read or decrypt keychains. Scan agent transcripts (spillscan does). Guess which provider an unrecognised key belongs to and send it there.
