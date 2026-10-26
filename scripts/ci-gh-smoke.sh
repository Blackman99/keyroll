#!/usr/bin/env bash
# keyroll ci against the real gh: it must explain, and exit 3, when gh cannot
# list secret names. Runs in Actions, whose GITHUB_TOKEN can never read them.
set -u
kr=${KR:-./kr}
repo=${GITHUB_REPOSITORY:-Blackman99/keyroll}
fail=0

expect() { # name, want exit, want text, command...
  local name=$1 want=$2 text=$3; shift 3
  local out code
  out=$("$@" 2>&1); code=$?
  if [ "$code" != "$want" ] || ! grep -qF -- "$text" <<<"$out"; then
    echo "FAIL $name: exit $code (want $want), want \"$text\" in:"; echo "$out"; fail=1
  else
    echo "ok   $name: $(head -1 <<<"$out")"
  fi
}

gh --version | head -1
expect "not logged in" 3 "gh auth login" env -u GH_TOKEN -u GITHUB_TOKEN GH_CONFIG_DIR="$(mktemp -d)" "$kr" ci --repo "$repo"
expect "bad token" 3 "Bad credentials" env GH_TOKEN=not-a-real-token GH_CONFIG_DIR="$(mktemp -d)" "$kr" ci --repo "$repo"
if [ -n "${GH_TOKEN:-}" ]; then
  expect "Actions token" 3 "GITHUB_TOKEN never can" "$kr" ci --repo "$repo"
fi
exit $fail
