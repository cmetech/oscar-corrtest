#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
tmp_base=${TMPDIR:-/tmp}
snapshot=$(mktemp "$tmp_base/oscar-corrtest-checksums.XXXXXX")
trap 'rm -f -- "$snapshot"' EXIT HUP INT TERM

make -C "$root" clean package checksums
cp "$root/dist/SHA256SUMS" "$snapshot"
make -C "$root" clean package checksums
cmp "$snapshot" "$root/dist/SHA256SUMS"
