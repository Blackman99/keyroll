#!/usr/bin/env bash
# Real credential stores: put fake entries where AI tools put theirs (the
# macOS Keychain, Windows Credential Manager, the Secret Service via GNOME
# Keyring), then check that keyroll lists them by name and never prints the
# value. Runs in CI on all three OSes. Linux needs: gnome-keyring,
# libsecret-tools (secret-tool, only to WRITE the fake entries) and a D-Bus
# session (run it under `dbus-run-session --`).
set -euo pipefail
cd "$(dirname "$0")/.."
go build -o kr-cs ./cmd/keyroll
KR="$PWD/kr-cs"
FAKE="FAKE-keyroll-credstore-value-$RANDOM$RANDOM"
export NO_COLOR=1
case "$(uname -s)" in
Darwin)
  T=$(mktemp -d); KC="$T/keyroll-smoke.keychain-db"
  OLD=$(security list-keychains -d user | tr -d '"' | xargs)
  security create-keychain -p smoke "$KC"
  security unlock-keychain -p smoke "$KC"
  # shellcheck disable=SC2086
  security list-keychains -d user -s "$KC" $OLD
  # shellcheck disable=SC2086
  trap 'security list-keychains -d user -s $OLD; security delete-keychain "$KC"; rm -rf "$T" "$KR"' EXIT
  security add-generic-password -s "Claude Code" -a "$USER" -w "$FAKE" "$KC"
  security add-internet-password -s "https://api.anthropic.com" -a Bearer -w "$FAKE" "$KC"
  want=("Claude Code" "https://api.anthropic.com")
  ;;
Linux)
  T=$(mktemp -d); export HOME="$T" XDG_DATA_HOME="$T/.local/share"   # the throwaway keyring lives here
  trap 'rm -rf "$T" "$KR"' EXIT
  eval "$(printf 'smoke\n' | gnome-keyring-daemon --unlock --components=secrets)" >/dev/null
  # the attributes keyring-rs (Goose) and Zed write
  printf '%s' "$FAKE" | secret-tool store --label='goose' service goose username secrets target default application rust-keyring
  printf '%s' "$FAKE" | secret-tool store --label='zed' url https://api.anthropic.com username Bearer
  want=("service=goose username=secrets" "url=https://api.anthropic.com")
  ;;
MINGW* | MSYS* | CYGWIN*)
  export MSYS_NO_PATHCONV=1
  trap 'cmdkey /delete:secrets.goose >/dev/null; cmdkey "/delete:zed:url=https://api.anthropic.com" >/dev/null; rm -f "$KR"' EXIT
  # the target names keyring-rs (Goose) and Zed write
  cmdkey /generic:secrets.goose /user:secrets "/pass:$FAKE" >/dev/null
  cmdkey "/generic:zed:url=https://api.anthropic.com" /user:Bearer "/pass:$FAKE" >/dev/null
  want=("secrets.goose" "zed:url=https://api.anthropic.com")
  ;;
*)
  echo "unsupported OS"; exit 1 ;;
esac

"$KR" --no-walk --provider anthropic | tee map.txt
"$KR" --no-walk --json > map.json
for w in "${want[@]}"; do
  grep -qF -- "$w" map.txt || { echo "map does not list $w"; exit 1; }
  grep -qF -- "$w" map.json || { echo "json does not list $w"; exit 1; }
done
grep -qF "credential store" map.txt
if grep -qF -- "$FAKE" map.txt map.json; then echo "LEAK: a credential-store value reached the output"; exit 1; fi
if "$KR" --no-walk --no-credstore | grep -qF "credential store"; then echo "--no-credstore still asked"; exit 1; fi
rm -f map.txt map.json
echo "credstore smoke: ok (listed by name, value never printed)"
