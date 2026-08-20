#!/bin/sh
set -eu

repository=cmetech/oscar-corrtest
release_base_url=${OSCAR_CORRTEST_RELEASE_BASE_URL:-https://github.com/$repository/releases/download}
release_api_url=${OSCAR_CORRTEST_RELEASE_API_URL:-https://api.github.com/repos/$repository/releases/latest}

fail() {
  printf 'oscar-corrtest install: %s\n' "$1" >&2
  exit 1
}

command -v uname >/dev/null 2>&1 || fail 'uname is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
if command -v curl >/dev/null 2>&1; then
  download() { curl -fsSL --retry 3 --connect-timeout 15 "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  download() { wget -q -O "$2" "$1"; }
else
  fail 'curl or wget is required'
fi

case "$(uname -s)" in
  Linux) os_name=linux ;;
  Darwin) os_name=darwin ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if [ -z "${HOME:-}" ]; then
  fail 'HOME is required for a user-scoped installation'
fi
install_dir=${OSCAR_CORRTEST_INSTALL_DIR:-$HOME/.local/bin}
case "$install_dir" in
  /*) ;;
  *) fail 'OSCAR_CORRTEST_INSTALL_DIR must be an absolute path' ;;
esac

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
work=$(mktemp -d "$tmp_base/oscar-corrtest-install.XXXXXX")
install_tmp=
cleanup() {
  if [ -n "$install_tmp" ] && [ -f "$install_tmp" ]; then
    rm -f -- "$install_tmp"
  fi
  case "$work" in
    "$tmp_base"/oscar-corrtest-install.*)
      if [ -d "$work" ]; then rm -rf -- "$work"; fi
      ;;
  esac
}
trap cleanup EXIT HUP INT TERM

version=${OSCAR_CORRTEST_VERSION:-}
if [ -z "$version" ]; then
  latest_json="$work/latest.json"
  download "$release_api_url" "$latest_json" || fail 'could not resolve the latest GitHub release'
  version=$(sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\(v[^"]*\)".*/\1/p' "$latest_json" | sed -n '1p')
fi
if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  fail "release version must be an exact semantic tag (vX.Y.Z): $version"
fi

asset="oscar-corrtest_${version}_${os_name}_${arch}.tar.gz"
archive="$work/$asset"
sums="$work/SHA256SUMS"
release_base_url=${release_base_url%/}
download "$release_base_url/$version/$asset" "$archive" || fail "could not download $asset"
download "$release_base_url/$version/SHA256SUMS" "$sums" || fail 'could not download SHA256SUMS'

expected=$(awk -v file="$asset" '$2 == file { count++; digest=$1 } END { if (count == 1) print digest; else exit 1 }' "$sums") || fail "SHA256SUMS must contain exactly one row for $asset"
if [ "${#expected}" -ne 64 ] || printf '%s\n' "$expected" | grep -Eq '[^0-9a-fA-F]'; then
  fail "invalid SHA-256 value for $asset"
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$archive" | awk '{print $1}')
else
  fail 'sha256sum or shasum is required'
fi
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
actual=$(printf '%s' "$actual" | tr 'A-F' 'a-f')
if [ "$actual" != "$expected" ]; then
  fail "SHA-256 mismatch for $asset"
fi

if ! tar -tzf "$archive" | awk '
  BEGIN { binary = 0 }
  {
    if (substr($0, 1, 1) == "/") exit 1
    count = split($0, parts, "/")
    for (i = 1; i <= count; i++) if (parts[i] == "..") exit 1
    if ($0 == "oscar-corrtest/bin/oscar-corrtest") binary++
  }
  END { if (binary != 1) exit 1 }
'; then
  fail 'archive has an unsafe path or does not contain exactly one expected executable'
fi
if ! tar -tvzf "$archive" | awk 'substr($0, 1, 1) == "l" || substr($0, 1, 1) == "h" { unsafe=1 } END { exit unsafe }'; then
  fail 'archive contains a symlink or hard link'
fi

stage="$work/stage"
mkdir -p "$stage"
tar -xzf "$archive" -C "$stage"
staged_binary="$stage/oscar-corrtest/bin/oscar-corrtest"
if [ ! -f "$staged_binary" ] || [ -L "$staged_binary" ] || [ ! -x "$staged_binary" ]; then
  fail 'staged executable is missing, non-regular, or not executable'
fi

umask 077
mkdir -p "$install_dir"
install_dir=$(CDPATH= cd -- "$install_dir" && pwd -P)
install_tmp=$(mktemp "$install_dir/.oscar-corrtest.XXXXXX")
install -m 0755 "$staged_binary" "$install_tmp"
mv -f -- "$install_tmp" "$install_dir/oscar-corrtest"
install_tmp=
if [ "$os_name" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$install_dir/oscar-corrtest" >/dev/null 2>&1 || :
fi

printf '\nInstalled oscar-corrtest %s at:\n  %s/oscar-corrtest\n' "$version" "$install_dir"
case ":${PATH:-}:" in
  *:"$install_dir":*) ;;
  *) printf '\nAdd %s to PATH, or use the absolute path below.\n' "$install_dir" ;;
esac
printf '\nThe installer did not start a service or change corrtest data.\n'
printf 'Start the UI explicitly:\n  %s/oscar-corrtest serve\n' "$install_dir"
printf 'Then open:\n  http://<server-ip>:8787\n'
printf '\nTo run OSCAR tests, provide only an external API key reference:\n'
printf "  export OSCAR_API_KEY='<your-api-key>'\n"
printf '  %s/oscar-corrtest target add --name lab-a --url https://oscar.example/ext/mw --credential-env OSCAR_API_KEY\n' "$install_dir"
