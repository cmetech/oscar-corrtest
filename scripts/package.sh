#!/bin/sh
set -eu

usage() {
  printf '%s\n' 'usage: package.sh <version> <amd64|arm64> <binary-path> <source-date-epoch>' >&2
}

if [ "$#" -ne 4 ]; then
  usage
  exit 2
fi

version=$1
arch=$2
binary=$3
source_date_epoch=$4

case "$arch" in
  amd64|arm64) ;;
  *)
    printf 'unsupported architecture: %s\n' "$arch" >&2
    exit 2
    ;;
esac

case "$source_date_epoch" in
  ''|*[!0-9]*)
    printf 'source-date-epoch must be numeric: %s\n' "$source_date_epoch" >&2
    exit 2
    ;;
esac

if [ ! -f "$binary" ]; then
  printf 'binary does not exist: %s\n' "$binary" >&2
  exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root_dir=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
if [ ! -f "$root_dir/README.md" ]; then
  printf '%s\n' 'README.md is required for packaging' >&2
  exit 1
fi

if command -v gtar >/dev/null 2>&1; then
  tar_bin=$(command -v gtar)
elif tar --version 2>/dev/null | grep -q 'GNU tar'; then
  tar_bin=$(command -v tar)
else
  printf '%s\n' 'GNU tar is required; install gtar (for example: brew install gnu-tar)' >&2
  exit 1
fi

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
stage=$(mktemp -d "$tmp_base/oscar-corrtest-package.XXXXXX")
case "$stage" in
  "$tmp_base"/oscar-corrtest-package.*) ;;
  *)
    printf 'refusing unexpected temporary path: %s\n' "$stage" >&2
    exit 1
    ;;
esac

cleanup() {
  case "$stage" in
    "$tmp_base"/oscar-corrtest-package.*)
      if [ -d "$stage" ]; then rm -rf -- "$stage"; fi
      ;;
  esac
}
trap cleanup EXIT HUP INT TERM

package_root="$stage/oscar-corrtest"
mkdir -p "$package_root/bin" "$package_root/docs/schema" "$package_root/packaging" "$root_dir/dist"
install -m 0755 "$binary" "$package_root/bin/oscar-corrtest"
install -m 0644 "$root_dir/README.md" "$package_root/README.md"
install -m 0644 "$root_dir/docs/operator.md" "$package_root/docs/operator.md"
install -m 0644 "$root_dir/docs/builtins.md" "$package_root/docs/builtins.md"
install -m 0644 "$root_dir/docs/live-qualification.md" "$package_root/docs/live-qualification.md"
install -m 0644 "$root_dir/docs/schema/correlation-scenario.schema.json" "$package_root/docs/schema/correlation-scenario.schema.json"
install -m 0644 "$root_dir/packaging/oscar-corrtest.service" "$package_root/packaging/oscar-corrtest.service"
install -m 0644 "$root_dir/Containerfile" "$package_root/Containerfile"

archive="$root_dir/dist/oscar-corrtest_${version}_linux_${arch}.tar.gz"
archive_tmp="$archive.tmp"
trap 'if [ -f "$archive_tmp" ]; then rm -f -- "$archive_tmp"; fi; cleanup' EXIT HUP INT TERM

"$tar_bin" \
  --format=gnu \
  --sort=name \
  --numeric-owner \
  --owner=0 \
  --group=0 \
  --mtime="@$source_date_epoch" \
  -C "$stage" \
  -cf - oscar-corrtest | gzip -n > "$archive_tmp"
mv "$archive_tmp" "$archive"
