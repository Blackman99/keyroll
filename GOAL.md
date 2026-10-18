# Goal

**Find every copy of your AI provider API keys on your machine, tell you which ones still work, and rotate a key everywhere at once.**

This sentence does not change. Features that do not serve it are declined, politely, with a link here.

## Who it is for
People who run more than one AI coding agent or AI CLI (Claude Code, Codex, OpenCode, Cline, Cursor, Gemini CLI, Aider, Continue, Goose, Crush, `llm`, …) and have pasted the same provider key into several of them, their shell files, project `.env` files and the backups and history those leave behind. The acute case: a provider or a hosted console leaks keys (OpenCode Console, 2026-10-17) and you have to revoke and replace one key in every place it lives, tonight, without missing one, and know at the end whether the old key is really dead.

## What it is
- `keyroll` — a map: each distinct key (by fingerprint, never by value) and every place a copy sits, labelled live / database / leftover.
- `keyroll check` — one free read-only request per key to the issuer's own API: **ALIVE** (accepted), **REJECTED** (revoked, deleted or mistyped) or unknown, plus spend-cap info where the provider exposes it.
- `keyroll rotate <fp>` — paste the new key once: live copies are rewritten in place, leftovers are scrubbed of the old key (never given the new one), databases get the exact command to run.
- `keyroll mcp` — the same map for an agent, read-only and offline.

## What it is not
- Not a secret manager, vault or proxy.
- Not a repository or git-history scanner (use gitleaks / trufflehog).
- Not a transcript scrubber: keys pasted into agent chats are [spillscan](https://github.com/Blackman99/spillscan)'s job.
- Not a "what can my agent reach" auditor (see [heldkeys](https://github.com/hugomn/heldkeys)).
- It never sends a key anywhere except, with `check`, to the provider that issued it.
