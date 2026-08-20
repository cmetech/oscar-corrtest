#!/bin/sh
set -eu

tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
scan_list=
archive_dir=

cleanup() {
  if [ -n "$scan_list" ]; then
    case "$scan_list" in
      "$tmp_base"/oscar-corrtest-scan.*)
        if [ -f "$scan_list" ]; then unlink "$scan_list"; fi
        ;;
    esac
  fi
  if [ -n "$archive_dir" ]; then
    case "$archive_dir" in
      "$tmp_base"/oscar-corrtest-standalone.*)
        if [ -d "$archive_dir" ]; then
          chmod -R u+w "$archive_dir"
          find "$archive_dir" -mindepth 1 -delete
          rmdir "$archive_dir"
        fi
        ;;
    esac
  fi
}
trap cleanup EXIT HUP INT TERM

new_scan_list() {
  scan_list=$(mktemp "$tmp_base/oscar-corrtest-scan.XXXXXX")
  case "$scan_list" in
    "$tmp_base"/oscar-corrtest-scan.*) ;;
    *)
      printf 'refusing unexpected scan-list path: %s\n' "$scan_list" >&2
      exit 1
      ;;
  esac
}

append_tree() {
  directory=$1
  if [ -d "$directory" ]; then
    find "$directory" -type f -print >> "$scan_list"
  fi
}

scan_root() {
  root=$1
  root=$(CDPATH= cd -- "$root" && pwd -P)
  new_scan_list

  find "$root" -type f -name '*.go' -print >> "$scan_list"
  for file in go.mod go.sum Makefile .gitlab-ci.yml; do
    if [ -f "$root/$file" ]; then printf '%s\n' "$root/$file" >> "$scan_list"; fi
  done
  append_tree "$root/scripts"
  append_tree "$root/.github/workflows"
  append_tree "$root/packaging"
  if [ -f "$root/Containerfile" ]; then printf '%s\n' "$root/Containerfile" >> "$scan_list"; fi
  append_tree "$root/internal/web/templates"
  append_tree "$root/internal/web/static"
  LC_ALL=C sort -u "$scan_list" -o "$scan_list"

  parent_ref='..'"/oscar"
  workspace_ref='/oscar_app/'"oscar"
  module_ref='github.com/cmetech/'"oscar/"
  failed=0
  while IFS= read -r file; do
    if grep -nH -F -e "$parent_ref" -e "$workspace_ref" -e "$module_ref" "$file"; then
      failed=1
    fi
  done < "$scan_list"

  unlink "$scan_list"
  scan_list=
  if [ "$failed" -ne 0 ]; then
    printf '%s\n' 'forbidden OSCAR reference in source/build inputs' >&2
    return 1
  fi
}

if [ "${1:-}" = "--scan-only" ]; then
  if [ "$#" -ne 2 ]; then
    printf '%s\n' 'usage: test-standalone.sh --scan-only <root>' >&2
    exit 2
  fi
  scan_root "$2"
  exit 0
fi

if [ "$#" -ne 0 ]; then
  printf '%s\n' 'usage: test-standalone.sh [--scan-only <root>]' >&2
  exit 2
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)

if [ -n "$(git -C "$root" status --porcelain)" ]; then
  printf '%s\n' 'standalone check requires a clean worktree' >&2
  exit 1
fi

scan_root "$root"

archive_dir=$(mktemp -d "$tmp_base/oscar-corrtest-standalone.XXXXXX")
case "$archive_dir" in
  "$tmp_base"/oscar-corrtest-standalone.*) ;;
  *)
    printf 'refusing unexpected archive path: %s\n' "$archive_dir" >&2
    exit 1
    ;;
esac

git -C "$root" archive HEAD | tar -xf - -C "$archive_dir"
symlink=$(find "$archive_dir" -type l -print -quit)
if [ -n "$symlink" ]; then
  printf 'archive contains forbidden symlink: %s\n' "$symlink" >&2
  exit 1
fi

scan_root "$archive_dir"
mkdir -p "$archive_dir/.cache/go-build" "$archive_dir/.cache/go-mod"
(
  cd "$archive_dir"
  GOWORK=off \
    GOCACHE="$archive_dir/.cache/go-build" \
    GOMODCACHE="$archive_dir/.cache/go-mod" \
    make archive-mod-check test build
  ./bin/oscar-corrtest version
  smoke_dir="$archive_dir/.smoke"
  mkdir -p "$smoke_dir"
  OSCAR_CORRTEST_STANDALONE_SECRET='must-not-be-persisted' \
    ./bin/oscar-corrtest target add \
      --data-dir "$smoke_dir/state" \
      --name standalone \
      --url https://oscar.invalid \
      --credential-env OSCAR_CORRTEST_STANDALONE_SECRET \
      --output json > "$smoke_dir/target.json"
  ./bin/oscar-corrtest target list --data-dir "$smoke_dir/state" --output json \
    | grep -F '"displayName":"standalone"'
  ./bin/oscar-corrtest runs list --data-dir "$smoke_dir/state" --output json \
    | grep -F '"apiVersion":"corrtest.oscar/v1alpha1"'
  ./bin/oscar-corrtest backup --data-dir "$smoke_dir/state" --output "$smoke_dir/backup.db"
  test -s "$smoke_dir/backup.db"
  if grep -R -a -F 'must-not-be-persisted' "$smoke_dir"; then
    printf '%s\n' 'standalone smoke test persisted a credential value' >&2
    exit 1
  fi
)
