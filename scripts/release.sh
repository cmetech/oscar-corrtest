#!/bin/sh
set -eu

usage() {
  printf '%s\n' 'usage: release.sh vMAJOR.MINOR.PATCH' >&2
  exit 2
}

fail() {
  printf 'release: %s\n' "$1" >&2
  exit 1
}

if [ "$#" -ne 1 ]; then
  usage
fi
version=$1
if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  usage
fi

for command_name in git make curl; do
  command -v "$command_name" >/dev/null 2>&1 || fail "$command_name is required"
done

root=$(git rev-parse --show-toplevel 2>/dev/null) || fail 'run this command inside the oscar-corrtest Git repository'
cd "$root"
if [ -n "$(git status --porcelain=v1 --untracked-files=normal)" ]; then
  fail 'the worktree must be clean before a release'
fi
branch=$(git symbolic-ref --quiet --short HEAD 2>/dev/null) || fail 'HEAD must be attached to branch main'
if [ "$branch" != main ]; then
  fail "release branch must be main, not $branch"
fi

remote=${OSCAR_CORRTEST_RELEASE_REMOTE:-origin}
remote_url=$(git config --get "remote.$remote.url" 2>/dev/null) || fail "release remote is not configured: $remote"
case "$remote_url" in
  https://github.com/cmetech/oscar-corrtest|https://github.com/cmetech/oscar-corrtest.git|git@github.com:cmetech/oscar-corrtest.git|ssh://git@github.com/cmetech/oscar-corrtest.git) ;;
  *) fail "release remote must point to github.com/cmetech/oscar-corrtest: $remote_url" ;;
esac

git fetch "$remote" "+refs/heads/main:refs/remotes/$remote/main" --tags || fail "could not fetch $remote main and tags"
head_commit=$(git rev-parse HEAD)
remote_commit=$(git rev-parse "refs/remotes/$remote/main" 2>/dev/null) || fail "$remote/main is unavailable"
if [ "$head_commit" != "$remote_commit" ]; then
  fail "local HEAD must equal $remote/main before release"
fi
if git rev-parse -q --verify "refs/tags/$version" >/dev/null; then
  fail "local tag already exists: $version"
fi
if git ls-remote --exit-code --tags "$remote" "refs/tags/$version" >/dev/null 2>&1; then
  fail "remote tag already exists: $version"
else
  remote_tag_status=$?
  if [ "$remote_tag_status" -ne 2 ]; then
    fail "could not verify the remote tag namespace for $version"
  fi
fi

tmp_base=${TMPDIR:-/tmp}
response=$(mktemp "$tmp_base/oscar-corrtest-release-response.XXXXXX")
cleanup() {
  case "$response" in
    "$tmp_base"/oscar-corrtest-release-response.*) rm -f -- "$response" ;;
  esac
}
trap cleanup EXIT HUP INT TERM

release_status=$(curl -sS -L -o "$response" -w '%{http_code}' "https://api.github.com/repos/cmetech/oscar-corrtest/releases/tags/$version") || fail 'could not query GitHub Releases'
case "$release_status" in
  404) ;;
  200) fail "GitHub release already exists: $version" ;;
  *) fail "GitHub release collision check returned HTTP $release_status" ;;
esac

make clean release-gate "VERSION=$version"

assets="oscar-corrtest_${version}_darwin_amd64.tar.gz
oscar-corrtest_${version}_darwin_arm64.tar.gz
oscar-corrtest_${version}_linux_amd64.tar.gz
oscar-corrtest_${version}_linux_arm64.tar.gz
oscar-corrtest_${version}_windows_amd64.zip"
for asset in $assets; do
  if [ ! -f "dist/$asset" ]; then
    fail "release gate did not produce dist/$asset"
  fi
  rows=$(grep -c "  $asset\$" dist/SHA256SUMS || true)
  if [ "$rows" -ne 1 ]; then
    fail "SHA256SUMS must contain exactly one row for $asset"
  fi
done
checksum_rows=$(wc -l < dist/SHA256SUMS | tr -d '[:space:]')
if [ "$checksum_rows" -ne 5 ]; then
  fail "SHA256SUMS must contain exactly five asset rows"
fi
if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum -c SHA256SUMS) || fail 'release asset checksum verification failed'
else
  (cd dist && shasum -a 256 -c SHA256SUMS) || fail 'release asset checksum verification failed'
fi

git tag -a "$version" -m "OSCAR Correlation Test Harness $version"
if ! git push "$remote" "refs/tags/$version:refs/tags/$version"; then
  printf 'release: tag push failed; the verified local tag was preserved\n' >&2
  printf 'retry with: git push %s refs/tags/%s:refs/tags/%s\n' "$remote" "$version" "$version" >&2
  exit 1
fi

printf 'Pushed %s. Follow publication at:\n' "$version"
printf '  https://github.com/cmetech/oscar-corrtest/actions/workflows/release.yml\n'
printf 'Release URL after the workflow succeeds:\n'
printf '  https://github.com/cmetech/oscar-corrtest/releases/tag/%s\n' "$version"
