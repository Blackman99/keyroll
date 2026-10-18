<p align="center"><img src="assets/logo-512.png" width="120" alt="keyroll logo: a switchboard plug whose tip is a key blade"></p>

<h1 align="center">keyroll</h1>

<p align="center"><b>Where else did I paste that key?</b><br>
Find every copy of your AI provider API keys on this machine, ask each issuer which ones still work, and rotate one key everywhere at once.</p>

<p align="center">
<a href="https://github.com/Blackman99/keyroll/actions/workflows/ci.yml"><img src="https://github.com/Blackman99/keyroll/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
<a href="https://github.com/Blackman99/keyroll/releases/latest"><img src="https://img.shields.io/github/v/release/Blackman99/keyroll?color=8fa597" alt="release"></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-17150f" alt="MIT"></a>
<a href="https://blackman99.github.io/keyroll/">website</a> · <a href="https://blackman99.github.io/keyroll/docs/">docs</a>
</p>

<p align="center"><img src="assets/demo.gif" width="860" alt="keyroll on a demo home: one Anthropic key in 9 places, the issuer rejects it, then rotate rewrites 3 live copies, scrubs 5 leftovers and ends by confirming the old key is revoked"></p>

## The problem

After a leak, the advice is always one line: *revoke the key and replace it everywhere.* Nobody tells you where "everywhere" is.

On 2026-10-17 OpenCode said its Console had been breached and that [third-party provider keys saved in a workspace may have been exposed](https://news.ycombinator.com/item?id=50133401). People reported charges on keys they had forgotten about. Anthropic said it had [revoked the Claude API keys it believed were affected](https://x.com/lydiahallie/status/2111563956666110287), so some agents started failing with 401s overnight. Others rotated by hand and then hit ["this API key has expired"](https://github.com/anomalyco/opencode/issues/56556), because a stale copy was still configured in another tool. One person [audited their Mac and reported 1 token in 7 copies](https://x.com/HeyAIpenguin/status/2111579446847156485), two of them made by Claude Code's automatic backups.

**Who was actually affected by the OpenCode breach?** According to [OpenCode's own post](https://x.com/opencode/status/2111519637049319818), 1.4% of OpenCode Console users: the subset who had linked external providers in the Console. Everyone affected got an email. Keys typed into the local OpenCode client reportedly never reached their servers (co-founder Dax, [as relayed here](https://x.com/Jarvist719/status/2111628993623388190); secondhand). If you were not emailed you were probably not part of the breach, but the same question remains after any leak: where does this key live, does it still work, and how do I change every copy?

keyroll answers those three questions:

- **`keyroll`** maps every copy of every key, offline: agent tool configs, app state, shell rc files and history, project `.env` files, and the backups those leave behind.
- **`keyroll check`** asks each issuer, with one free read-only request, whether each key is **ALIVE** (accepted) or **REJECTED** (revoked, deleted or mistyped). If Claude Code started throwing 401s, this tells you it is the issuer, not a bug in your code.
- **`keyroll rotate <fp>`** takes the new key once, rewrites every live copy, scrubs the old key out of leftovers (and does *not* copy the new key into them), backs up the originals, and **ends by asking the issuer whether the old key is actually revoked**.

Keys you pasted into agent *chats* sit in session transcripts (`~/.claude/projects/**/*.jsonl`, `~/.codex/sessions`). keyroll does not read those; that is what [spillscan](https://github.com/Blackman99/spillscan) is for, and keyroll's output points you there.

## Try it in 10 seconds

```sh
curl -fsSL https://blackman99.github.io/keyroll/install.sh | sh   # or: go install github.com/Blackman99/keyroll/cmd/keyroll@latest
keyroll
```

The map is offline. Nothing leaves your machine until you run `keyroll check`, and even then each key goes only to the issuer that minted it.

Here is the map on the fake demo home in this repo (`go run ./internal/testhome/demohome /tmp/demo && HOME=/tmp/demo keyroll --provider anthropic`):

```text
keyroll  1 key · 9 copies · 5 tools · offline, nothing sent

  ● Anthropic         sk-ant-…M1AA     #38bb1575  9 copies  3 live · 1 database · 5 leftovers
      live      Claude Code      ~/.claude.json:4                                    primaryApiKey
      live      shell            ~/.zshrc:6                                          ANTHROPIC_API_KEY
      live      project .env     ~/code/shop/.env:2                                  ANTHROPIC_API_KEY
      database  OpenCode         ~/.local/share/opencode/opencode.db
                                 → opencode auth login anthropic  (then: opencode auth list)
      leftover  OpenCode         ~/.local/share/opencode/auth.json:4                 anthropic.key
                                 legacy file: OpenCode v2 imported it into opencode.db and no longer reads it
      leftover  shell history    ~/.zsh_history:2
      leftover  Claude Code      ~/.claude/backups/.claude.json.backup.1760650211:4  primaryApiKey
                                 automatic backup of ~/.claude.json
      leftover  Claude Code      ~/.claude/backups/.claude.json.backup.1760736622:4  primaryApiKey
                                 automatic backup of ~/.claude.json
      leftover  project .env     ~/code/shop/.env.example:2                          ANTHROPIC_API_KEY
                                 template file: a real key should not be here

  next  keyroll check             ask each issuer: is each key ALIVE or REJECTED? (one free GET per key)
        keyroll rotate 38bb1575   paste the new key once: 3 files rewritten, 5 leftovers scrubbed, 1 by hand
  not here  keys pasted into agent chats live in session transcripts: github.com/Blackman99/spillscan
  scanned 17 files and 4 folders in 0.01s
```

Keys are shown as issuer, public prefix and last four characters (the same hint the Anthropic and OpenAI dashboards show) plus an 8-hex fingerprint of the SHA-256. **A key value is never printed, logged, written to JSON or returned over MCP.** A test greps every output mode for any 8-character slice of every fixture key.

## Commands

### `keyroll` (map)

| class | what it means | what `rotate` does |
|---|---|---|
| **live** | a tool reads this file: settings, `auth.json`, `.env`, rc files | writes the new key, byte for byte in place |
| **leftover** | nothing reads it, but it still unlocks your account: Claude Code backups, OpenCode's pre-v2 `auth.json`, shell history, `.env.example`, `*.bak`, editor Local History | **scrubs** the old key (`[revoked-by-keyroll:<fp>]`) and does *not* add the new one, so rotation shrinks the number of copies |
| **database** | OpenCode v2's `opencode.db` (and `-wal`) | never edited; prints the exact command (`opencode auth login anthropic`) |

Files tracked by git are flagged: the old key stays in history, so revoke it. Symlinked dotfiles are shown with their target and written through.

### `keyroll check [fp…]`

```text
keyroll check  5 keys · 5 requests · each key went only to its own issuer

  ✕ REJECTED  Anthropic         sk-ant-…M1AA     #38bb1575  9 copies   refused, HTTP 401
  ● ALIVE     OpenRouter        sk-or-v1-…7bdc   #8e460503  5 copies   no spend limit on this key, used $412.20

  ALIVE    the issuer accepts it: whoever holds a copy can spend on it.
  REJECTED the issuer refuses it: revoked, deleted or mistyped (issuers answer all three the same
           way). A tool still using it fails with 401 / "invalid x-api-key" / "key expired": that is the
           issuer, not a bug in your code.

  ! Anthropic sk-ant-…M1AA is refused, but Claude Code, shell, project .env and OpenCode still use
    it: their next request fails with HTTP 401.
    keyroll rotate 38bb1575  (paste the new key once)
```

(OpenRouter's answer above is simulated; the Anthropic 401 is what the real API returns for the fake key.)

- One `GET` per key to a free metadata endpoint on the issuer's own API host (`/v1/models`, OpenRouter `/api/v1/key`, DeepSeek `/user/balance`, …). The key travels in a header, never in a URL (Google: `x-goog-api-key`, not `?key=`). Redirects are not followed.
- **REJECTED** only on a 401 or the issuer's documented invalid-key answer (Google `400 API_KEY_INVALID`, xAI `400`). A bare 429, any other 403, 5xx, timeouts and network errors are **unknown**, never rejected. A 402 or an out-of-credit 429 means the key works.
- OpenRouter reports the key's spend limit; keyroll flags keys with **no spend limit**.
- Exit code `1` when a rejected key is still configured somewhere live, so it works in scripts.
- Login tokens a tool refreshes itself (Claude Code OAuth `sk-ant-oat…`) and admin keys are mapped but not sent anywhere.

### `keyroll rotate <fp>`

```sh
keyroll rotate 38bb1575 --dry-run      # the plan, nothing changed
keyroll rotate 38bb1575                # asks for the new key with echo off
pbpaste | keyroll rotate 38bb1575 --yes
```

1. The new key is read from a hidden prompt or stdin, never from an argument (that would land in shell history). keyroll checks it has the right shape for that issuer (an OpenAI key where an Anthropic one belongs is refused) and, unless `--offline`, that the issuer accepts it. Nothing is written otherwise.
2. Only the old key's bytes are replaced. Comments, key order and every other value stay as they were, so `~/.claude.json` stays valid JSON and your `.zshrc` diff is one line.
3. Each file is written atomically (temp file in the same folder, original permissions, fsync, rename). A file that changed since the scan is left untouched.
4. Originals go to `~/.keyroll/backups/<time>-<fp>/` (folder `700`, files `600`).
5. keyroll scans again and says how many places still hold the old key and what is left by hand.
6. **The last line is always the old key's status.** keyroll asks the issuer about the old key: `✓ … now REFUSES it … revoked`, or `✕ … still ACCEPTS it … NOT revoked yet` (copies keyroll cannot see, like a hosted console, CI secrets or another machine, can still spend on it), or, with `--offline`, *treat it as NOT revoked*. Revoking is a dashboard click keyroll cannot do for you; it prints the link.

### `keyroll paths`, `keyroll mcp`

- `keyroll paths` lists every fixed location keyroll reads and which exist on this machine.
- `keyroll mcp` is a read-only stdio MCP server with two tools, `key_map` and `key_copies`. They return fingerprints and file paths, never key material, and there is no tool that writes. Rotation stays a human step.

```sh
claude mcp add keyroll -- keyroll mcp
```

Flags: `--json`, `--provider anthropic`, `--root DIR` (walk more folders, repeatable), `--depth N`, `--no-walk`, `--file PATH`, `--timeout 10s`. Exit codes: `0` ok · `1` action needed · `2` usage · `3` error.

## What it covers

| where | files (`keyroll paths` has the full list for your OS) |
|---|---|
| Claude Code | `~/.claude.json`, `~/.claude/settings*.json`, `~/.claude/.credentials.json`, **`~/.claude/backups/*`** (leftovers) |
| Codex | `~/.codex/auth.json`, `config.toml`, `.env` |
| OpenCode | `~/.config/opencode/opencode.json(c)`, legacy `~/.local/share/opencode/auth.json` (leftover once `opencode.db` exists), **`opencode.db` + `-wal`** (database: by hand) |
| Cline, Continue, Cursor, Windsurf, Zed, VS Code / VSCodium | secrets and settings files, `mcp.json` |
| Gemini CLI, Aider, Goose, Crush, Pi, aichat, `llm`, Claude Desktop | their config / key files |
| Shell | `.zshrc`, `.zshenv`, `.zprofile`, `.bashrc`, `.bash_profile`, `.profile`, `.envrc`, `~/.env`, `~/.secrets`, `.netrc`, fish config and universal variables, PowerShell profiles |
| History (leftovers) | zsh, bash, fish, PowerShell, Python and Node REPL history |
| Projects | a walk of your home (or `--root`) for `.env*`, `.dev.vars`, `.envrc`, `opencode.json`, `.aider.conf.yml`, `.mcp.json`, `.claude/settings.local.json`, editor Local History copies; `*.bak` / `~` / `.orig` copies as leftovers |
| Issuers | Anthropic, OpenAI, OpenRouter, Google AI Studio, xAI, DeepSeek, Groq, Mistral, Together, Cerebras, Fireworks, Moonshot, Z.ai, Hugging Face (checked); Perplexity (mapped only, no free endpoint) |

**Not covered:** agent chat transcripts ([spillscan](https://github.com/Blackman99/spillscan)), git history ([gitleaks](https://github.com/gitleaks/gitleaks) / [trufflehog](https://github.com/trufflesecurity/trufflehog)), OS keychains, and secrets stored in CI providers (they are not on your disk; a `gh secret list` inventory is on the [roadmap](ROADMAP.md)). [heldkeys](https://github.com/hugomn/heldkeys) answers a related question, "which keys can my agent reach right now".

## Install

```sh
# macOS / Linux: latest release binary, checked against checksums.txt, into ~/.local/bin
curl -fsSL https://blackman99.github.io/keyroll/install.sh | sh

# any OS with Go 1.24+
go install github.com/Blackman99/keyroll/cmd/keyroll@latest
```

Or download a binary for macOS, Linux or Windows (amd64 / arm64) from [Releases](https://github.com/Blackman99/keyroll/releases/latest) and verify it against `checksums.txt`. One static binary, Go standard library only, no third-party dependencies: a tool that reads every API key on your machine should be easy to audit. [Read install.sh](site/install.sh) before you pipe it to `sh`; it is 49 lines.

## Development

```sh
go test ./...                 # unit + end-to-end tests on a fake home
./scripts/integration.sh      # real Codex and llm CLIs write keys, keyroll rotates them, the tools read the new key back
```

See [GOAL.md](GOAL.md), [ROADMAP.md](ROADMAP.md) and [CONTRIBUTING.md](CONTRIBUTING.md). Security problems: please use GitHub's private vulnerability reporting.

MIT © Blackman99
