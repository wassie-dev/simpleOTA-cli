#!/usr/bin/env bash
# Test install.sh and simpleota update against a goreleaser snapshot served
# from a local mirror.
#
#   goreleaser release --snapshot --clean --skip=publish
#   scripts/test-install.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="$ROOT/dist"
VERSION="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$DIST/metadata.json")"
WORK="$(mktemp -d)"
PORT="${PORT:-8766}"
trap 'kill "$SERVER" 2>/dev/null || true; rm -rf "$WORK"' EXIT

mkdir -p "$WORK/www/v$VERSION" "$WORK/www/v9.9.9"
cp "$DIST"/*.tar.gz "$DIST"/*.zip "$DIST/checksums.txt" "$WORK/www/v$VERSION/"
# A release whose checksums do not match its archives.
for f in "$DIST"/*.tar.gz; do
  name="$(basename "$f" | sed "s/_${VERSION}_/_9.9.9_/")"
  cp "$f" "$WORK/www/v9.9.9/$name"
  echo "0000000000000000000000000000000000000000000000000000000000000000  $name" >>"$WORK/www/v9.9.9/checksums.txt"
done
(cd "$WORK/www" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >/dev/null 2>&1) &
SERVER=$!
for _ in $(seq 1 50); do curl -fs "http://127.0.0.1:$PORT/" >/dev/null && break; sleep 0.1; done

DIR="$WORK/bin"
# The installer runs with a scratch HOME and shell, so it edits no real
# startup file.
H="$WORK/home"
mkdir -p "$H"
export SIMPLEOTA_DOWNLOAD_URL="http://127.0.0.1:$PORT" SIMPLEOTA_INSTALL_DIR="$DIR"
fail() { echo "FAIL: $*" >&2; exit 1; }
run() { HOME="${RUN_HOME:-$H}" SHELL=/bin/zsh ZDOTDIR="" SIMPLEOTA_VERSION="$1" sh "$ROOT/install.sh" 2>&1; }

out="$(run "$VERSION")" || fail "fresh install: $out"
grep -q "Installed simpleota $VERSION" <<<"$out" || fail "fresh install output: $out"
grep -q "Added $DIR to your PATH in $H/.zshrc" <<<"$out" || fail "PATH setup output: $out"
grep -qxF "  export PATH=\"$DIR:\$PATH\"" <<<"$out" || fail "no command for the current terminal: $out"
[ "$(grep -cxF "export PATH=\"$DIR:\$PATH\"" "$H/.zshrc")" = 1 ] || fail "PATH line in .zshrc: $(cat "$H/.zshrc")"
"$DIR/simpleota" --version | grep -q "simpleota version $VERSION" || fail "installed binary version"
echo "ok   fresh install adds the directory to PATH once"

out="$(run "v$VERSION")" || fail "re-run: $out"
grep -q "already installed" <<<"$out" || fail "re-run output: $out"
[ "$(grep -c "simpleota installer" "$H/.zshrc")" = 1 ] || fail "re-run added the PATH line again: $(cat "$H/.zshrc")"
echo "ok   re-run is a no-op"

out="$(RUN_HOME="$WORK/home2" SIMPLEOTA_NO_MODIFY_PATH=1 run "$VERSION")" || fail "no-modify run: $out"
[ ! -e "$WORK/home2/.zshrc" ] || fail "SIMPLEOTA_NO_MODIFY_PATH=1 wrote $WORK/home2/.zshrc"
grep -q "export PATH=" <<<"$out" || fail "no-modify hint: $out"
echo "ok   SIMPLEOTA_NO_MODIFY_PATH leaves startup files alone"

printf '#!/bin/sh\necho "simpleota version 0.0.1"\n' >"$DIR/simpleota"
chmod +x "$DIR/simpleota"
out="$(run "$VERSION")" || fail "update: $out"
grep -q "Updated simpleota from 0.0.1 to $VERSION" <<<"$out" || fail "update output: $out"
"$DIR/simpleota" --version | grep -q "$VERSION" || fail "updated binary version"
echo "ok   update replaces an older binary"

before="$(shasum -a 256 "$DIR/simpleota")"
if out="$(run 9.9.9)"; then fail "checksum mismatch accepted: $out"; fi
grep -q "checksum mismatch" <<<"$out" || fail "mismatch output: $out"
[ "$(shasum -a 256 "$DIR/simpleota")" = "$before" ] || fail "binary changed after a failed install"
echo "ok   checksum mismatch refused, binary untouched"

if out="$(run 1.2.3)"; then fail "missing version accepted: $out"; fi
grep -q "download failed" <<<"$out" || fail "missing version output: $out"
echo "ok   unknown version refused"

(cd "$ROOT" && go build -ldflags "-X github.com/wassie-dev/simpleOTA-cli/internal/version.Version=0.0.1" -o "$DIR/simpleota" ./cmd/simpleota)
out="$("$DIR/simpleota" update --version "$VERSION" 2>&1)" || fail "self-update: $out"
grep -q "Updated simpleota from 0.0.1 to $VERSION" <<<"$out" || fail "self-update output: $out"
"$DIR/simpleota" --version | grep -q "simpleota version $VERSION" || fail "self-updated binary version"
echo "ok   simpleota update replaces its own binary"

if out="$(HOME="$H" SIMPLEOTA_DOWNLOAD_URL=http://example.com SIMPLEOTA_VERSION="$VERSION" sh "$ROOT/install.sh" 2>&1)"; then fail "plain http mirror accepted"; fi
grep -q "must be https" <<<"$out" || fail "http mirror output: $out"
echo "ok   plain http mirror refused"
