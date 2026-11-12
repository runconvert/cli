#!/bin/sh
# Installs the RunConvert CLI on macOS and Linux:
#   curl -fsSL https://www.runconvert.com/install.sh | sh
#
# Settings (environment variables):
#   RUNCONVERT_VERSION      a version like 0.1.0 (default: the latest release)
#   RUNCONVERT_INSTALL_DIR  where to put runconvert (default: ~/.local/bin)
#   RUNCONVERT_DOWNLOAD_URL where releases are downloaded from
set -eu

repo_url=${RUNCONVERT_DOWNLOAD_URL:-https://github.com/runconvert/cli/releases}
dir=${RUNCONVERT_INSTALL_DIR:-$HOME/.local/bin}

say() { printf '%s\n' "$*"; }
fail() { printf 'runconvert install: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "this script is for macOS and Linux. On Windows: irm https://www.runconvert.com/install.ps1 | iex" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "no build for $(uname -m) yet" ;;
esac

if [ -n "${RUNCONVERT_VERSION:-}" ]; then
  base="$repo_url/download/v${RUNCONVERT_VERSION#v}"
else
  base="$repo_url/latest/download"
fi
name="runconvert_${os}_${arch}.tar.gz"

if command -v curl >/dev/null 2>&1; then
  get() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  get() { wget -q "$1" -O "$2"; }
else
  fail "needs curl or wget"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading runconvert for ${os}/${arch}..."
get "$base/$name" "$tmp/$name" || fail "download failed: $base/$name"
get "$base/checksums.txt" "$tmp/checksums.txt" || fail "download failed: $base/checksums.txt"

# Check the download against the published checksum before installing it.
want=$(grep " $name\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || fail "$name is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$name" | cut -d' ' -f1)
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $name (expected $want, got $got)"

tar -xzf "$tmp/$name" -C "$tmp" runconvert
mkdir -p "$dir"
mv "$tmp/runconvert" "$dir/runconvert"
chmod 755 "$dir/runconvert"

say "✓ Installed $("$dir/runconvert" --version) in $dir"
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    say ""
    say "Add $dir to your PATH, e.g. for zsh:"
    say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.zshrc && exec zsh"
    ;;
esac
say ""
say "Next:  runconvert login    then    runconvert"
