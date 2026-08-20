#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  printf '%s\n' 'usage: test-install-posix.sh <version>' >&2
  exit 2
fi

version=$1
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)

case "$(uname -s)" in
  Linux) os_name=linux ;;
  Darwin) os_name=darwin ;;
  *) printf '%s\n' 'POSIX installer smoke requires Linux or macOS' >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) printf '%s\n' 'POSIX installer smoke requires amd64 or arm64' >&2; exit 2 ;;
esac

asset="oscar-corrtest_${version}_${os_name}_${arch}.tar.gz"
source_asset="$root/dist/$asset"
source_sums="$root/dist/SHA256SUMS"
if [ ! -f "$source_asset" ] || [ ! -f "$source_sums" ]; then
  printf '%s\n' 'release assets must be built before the installer smoke' >&2
  exit 1
fi

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
work=$(mktemp -d "$tmp_base/oscar-corrtest-install-test.XXXXXX")
cleanup() {
  case "$work" in
    "$tmp_base"/oscar-corrtest-install-test.*) rm -rf -- "$work" ;;
  esac
}
trap cleanup EXIT HUP INT TERM

release_root="$work/releases"
release_dir="$release_root/$version"
home_dir="$work/home"
install_dir="$home_dir/bin"
state_dir="$home_dir/.local/state/oscar-corrtest"
mkdir -p "$release_dir" "$install_dir" "$state_dir"
cp "$source_asset" "$release_dir/$asset"
cp "$source_sums" "$release_dir/SHA256SUMS"
printf '%s\n' 'preserve this state' > "$state_dir/sentinel"
state_before=$(cksum "$state_dir/sentinel")

run_installer() {
  HOME="$home_dir" \
    OSCAR_CORRTEST_VERSION="$version" \
    OSCAR_CORRTEST_INSTALL_DIR="$install_dir" \
    OSCAR_CORRTEST_RELEASE_BASE_URL="file://$release_root" \
    sh "$root/scripts/install.sh"
}

run_installer
installed="$install_dir/oscar-corrtest"
if [ ! -x "$installed" ]; then
  printf '%s\n' 'installer did not create an executable' >&2
  exit 1
fi
"$installed" version | grep -F "oscar-corrtest $version " >/dev/null
installed_before=$(cksum "$installed")
run_installer
installed_after=$(cksum "$installed")
if [ "$installed_before" != "$installed_after" ]; then
  printf '%s\n' 'idempotent reinstall changed the executable' >&2
  exit 1
fi

cp "$source_asset" "$release_dir/$asset"
printf '%s' 'corrupt' >> "$release_dir/$asset"
if run_installer >/dev/null 2>&1; then
  printf '%s\n' 'installer accepted a corrupt archive' >&2
  exit 1
fi
if [ "$(cksum "$installed")" != "$installed_before" ]; then
  printf '%s\n' 'checksum failure changed the installed executable' >&2
  exit 1
fi

cp "$source_asset" "$release_dir/$asset"
grep -Fv "  $asset" "$source_sums" > "$release_dir/SHA256SUMS"
if run_installer >/dev/null 2>&1; then
  printf '%s\n' 'installer accepted a checksum file without the selected asset' >&2
  exit 1
fi
if [ "$(cksum "$installed")" != "$installed_before" ]; then
  printf '%s\n' 'missing checksum row changed the installed executable' >&2
  exit 1
fi
if [ "$(cksum "$state_dir/sentinel")" != "$state_before" ]; then
  printf '%s\n' 'installer changed persistent state' >&2
  exit 1
fi

printf '%s\n' 'POSIX installer smoke passed'
