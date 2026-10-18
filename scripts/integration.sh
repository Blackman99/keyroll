#!/usr/bin/env bash
# Real-tool integration test: the files are written by the real Codex CLI and
# the real `llm` CLI, keyroll finds and rotates them, and the tools read the new
# key back. Then `keyroll check` asks the real provider APIs about fake keys.
# Needs: go, codex (npm i -g @openai/codex), llm (pip install llm), python3.
set -euo pipefail
cd "$(dirname "$0")/.."
KR="$PWD/keyroll-it"
go build -o "$KR" ./cmd/keyroll
key() { DEMOHOME_KEY="$1" go run ./internal/testhome/demohome; }
OLD=$(key openai); NEW=$(key new-openai)
H="$PWD/.it-home"; rm -rf "$H"; mkdir -p "$H/.codex" "$H/code/app"
export HOME="$H" NO_COLOR=1
unset OPENAI_API_KEY CODEX_HOME LLM_USER_PATH || true

echo "== real tools write the key"
printf '%s' "$OLD" | codex login --with-api-key >/dev/null 2>&1
codex login status 2>&1 | tee /dev/stderr | grep -q "${OLD: -5}"
llm keys set openai --value "$OLD"
printf 'OPENAI_API_KEY=%s\n' "$OLD" > "$H/code/app/.env"

echo "== keyroll finds every copy"
"$KR" --provider openai
"$KR" --provider openai --json > map.json
python3 - "$H" <<'PY'
import json, sys
home = sys.argv[1]
rep = json.load(open("map.json"))
assert len(rep["keys"]) == 1, rep
paths = sorted(c["display"] for c in rep["keys"][0]["copies"])
print("copies:", paths)
for want in ["~/.codex/auth.json", "~/code/app/.env"]:
    assert want in paths, (want, paths)
assert any(p.endswith("io.datasette.llm/keys.json") for p in paths), paths
assert all(c["class"] == "live" for c in rep["keys"][0]["copies"])
raw = open("map.json").read()
PY
if grep -q "${OLD:20:16}" map.json; then echo "LEAK: key material in JSON"; exit 1; fi
FP=$(python3 -c 'import json;print(json.load(open("map.json"))["keys"][0]["fingerprint"])')

echo "== rotate"
printf '%s\n' "$NEW" | "$KR" rotate "$FP" --offline --yes

echo "== the real tools read the new key"
codex login status 2>&1 | tee /dev/stderr | grep -q "${NEW: -5}"
test "$(llm keys get openai)" = "$NEW"
grep -q "OPENAI_API_KEY=$NEW" "$H/code/app/.env"
test "$(stat -c %a "$H/.codex/auth.json")" = 600
"$KR" --provider openai --json | python3 -c 'import json,sys; r=json.load(sys.stdin); live=[k for k in r["keys"] if any(c["class"]=="live" for c in k["copies"])]; assert len(live)==1 and len(live[0]["copies"])==3, r; print("new key in 3 live places, old key only in keyroll backups")'

echo "== keyroll check against the real provider APIs (fake keys must come back rejected)"
H2="$PWD/.it-home2"; rm -rf "$H2"; go run ./internal/testhome/demohome "$H2"
set +e; HOME="$H2" "$KR" check; HOME="$H2" "$KR" check --json > check.json; code=$?; set -e
test "$code" = 1 || { echo "check should exit 1 (rejected keys still configured), got $code"; exit 1; }
python3 - <<'PY'
import json
rows = json.load(open("check.json"))
states = {r["provider"]: r["check"]["state"] for r in rows}
print(states)
for p in ["anthropic", "openai", "openrouter", "google", "deepseek"]:
    assert states.get(p) == "rejected", (p, states)
PY
rm -rf "$H" "$H2" map.json check.json "$KR"
echo "integration: ok"
