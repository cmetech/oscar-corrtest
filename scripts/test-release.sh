#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
release_script="$root/scripts/release.sh"
tmp_base=${TMPDIR:-/tmp}
tmp_base=${tmp_base%/}
work=$(mktemp -d "$tmp_base/oscar-corrtest-release-test.XXXXXX")
cleanup() {
  case "$work" in
    "$tmp_base"/oscar-corrtest-release-test.*) rm -rf -- "$work" ;;
  esac
}
trap cleanup EXIT HUP INT TERM

fake_bin="$work/fake-bin"
mkdir -p "$fake_bin"

make_fake_tools() {
  make_path="$fake_bin/make"
  curl_path="$fake_bin/curl"
  printf '%s\n' '#!/bin/sh' > "$make_path"
  printf '%s\n' 'set -eu' >> "$make_path"
  printf '%s\n' 'printf "%s\n" "$*" >> "$RELEASE_TEST_LOG"' >> "$make_path"
  printf '%s\n' 'if [ "${FAKE_MAKE_FAIL:-}" = 1 ]; then exit 9; fi' >> "$make_path"
  printf '%s\n' 'version=' >> "$make_path"
  printf '%s\n' 'for argument in "$@"; do case "$argument" in VERSION=*) version=${argument#VERSION=} ;; esac; done' >> "$make_path"
  printf '%s\n' 'test -n "$version"' >> "$make_path"
  printf '%s\n' 'mkdir -p dist' >> "$make_path"
  printf '%s\n' 'for suffix in darwin_amd64.tar.gz darwin_arm64.tar.gz linux_amd64.tar.gz linux_arm64.tar.gz windows_amd64.zip; do printf "%s %s\n" "$version" "$suffix" > "dist/oscar-corrtest_${version}_${suffix}"; done' >> "$make_path"
  printf '%s\n' 'cd dist' >> "$make_path"
  printf '%s\n' 'if command -v sha256sum >/dev/null 2>&1; then sha256sum oscar-corrtest_* > SHA256SUMS; else shasum -a 256 oscar-corrtest_* > SHA256SUMS; fi' >> "$make_path"

  printf '%s\n' '#!/bin/sh' > "$curl_path"
  printf '%s\n' 'set -eu' >> "$curl_path"
  printf '%s\n' 'output=' >> "$curl_path"
  printf '%s\n' 'while [ "$#" -gt 0 ]; do case "$1" in -o) output=$2; shift 2 ;; -w) shift 2 ;; *) shift ;; esac; done' >> "$curl_path"
  printf '%s\n' 'if [ -n "$output" ]; then : > "$output"; fi' >> "$curl_path"
  printf '%s\n' 'printf "%s" "${FAKE_CURL_STATUS:-404}"' >> "$curl_path"
  chmod +x "$make_path" "$curl_path"
}

make_fake_tools

case_number=0
setup_repository() {
  case_number=$((case_number + 1))
  case_root="$work/case-$case_number"
  bare="$case_root/origin.git"
  repo="$case_root/repo"
  mkdir -p "$case_root"
  git init --bare -q "$bare"
  git init -q -b main "$repo"
  git -C "$repo" config user.name 'Release Test'
  git -C "$repo" config user.email 'release-test@example.invalid'
  git -C "$repo" config "url.file://$bare.insteadOf" 'https://github.com/cmetech/oscar-corrtest.git'
  git -C "$repo" remote add origin 'https://github.com/cmetech/oscar-corrtest.git'
  printf '%s\n' 'test repository' > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" commit -q -m initial
  git -C "$repo" push -q -u origin main
  log="$case_root/make.log"
  output="$case_root/output.log"
  : > "$log"
}

run_release() {
  set +e
  (cd "$repo" && PATH="$fake_bin:$PATH" RELEASE_TEST_LOG="$log" FAKE_CURL_STATUS="${FAKE_CURL_STATUS:-404}" sh "$release_script" "$@") > "$output" 2>&1
  status=$?
  set -e
}

assert_no_tag() {
  if git -C "$repo" rev-parse -q --verify "refs/tags/$1" >/dev/null; then
    printf 'unexpected tag created: %s\n' "$1" >&2
    exit 1
  fi
}

setup_repository
run_release 1.2.3
if [ "$status" -ne 2 ]; then printf 'invalid version exit=%s\n' "$status" >&2; exit 1; fi
assert_no_tag v1.2.3

setup_repository
printf '%s\n' dirty > "$repo/untracked"
run_release v1.2.3
if [ "$status" -eq 0 ]; then printf '%s\n' 'dirty tree was accepted' >&2; exit 1; fi
assert_no_tag v1.2.3

setup_repository
git -C "$repo" switch -q -c feature
run_release v1.2.3
if [ "$status" -eq 0 ]; then printf '%s\n' 'non-main branch was accepted' >&2; exit 1; fi
assert_no_tag v1.2.3

setup_repository
printf '%s\n' ahead >> "$repo/README.md"
git -C "$repo" commit -q -am ahead
run_release v1.2.3
if [ "$status" -eq 0 ]; then printf '%s\n' 'HEAD differing from origin/main was accepted' >&2; exit 1; fi
assert_no_tag v1.2.3

setup_repository
git -C "$repo" tag -a v1.2.3 -m existing
run_release v1.2.3
if [ "$status" -eq 0 ]; then printf '%s\n' 'existing local tag was accepted' >&2; exit 1; fi

setup_repository
git -C "$repo" tag -a v1.2.3 -m remote-existing
git -C "$repo" push -q origin refs/tags/v1.2.3
git -C "$repo" tag -d v1.2.3 >/dev/null
run_release v1.2.3
if [ "$status" -eq 0 ]; then printf '%s\n' 'existing remote tag was accepted' >&2; exit 1; fi

setup_repository
FAKE_CURL_STATUS=200
run_release v1.2.3
unset FAKE_CURL_STATUS
if [ "$status" -eq 0 ]; then printf '%s\n' 'existing GitHub release was accepted' >&2; exit 1; fi
assert_no_tag v1.2.3

setup_repository
FAKE_MAKE_FAIL=1
export FAKE_MAKE_FAIL
run_release v1.2.3
unset FAKE_MAKE_FAIL
if [ "$status" -eq 0 ]; then printf '%s\n' 'failed release gate was accepted' >&2; exit 1; fi
assert_no_tag v1.2.3

setup_repository
remote_main_before=$(git --git-dir="$bare" rev-parse refs/heads/main)
run_release v1.2.3
if [ "$status" -ne 0 ]; then cat "$output" >&2; exit 1; fi
if [ "$(cat "$log")" != 'clean release-gate VERSION=v1.2.3' ]; then
  printf 'unexpected make invocation: %s\n' "$(cat "$log")" >&2
  exit 1
fi
if [ "$(git -C "$repo" cat-file -t refs/tags/v1.2.3)" != tag ]; then
  printf '%s\n' 'release tag is not annotated' >&2
  exit 1
fi
git --git-dir="$bare" rev-parse -q --verify refs/tags/v1.2.3 >/dev/null
remote_main_after=$(git --git-dir="$bare" rev-parse refs/heads/main)
if [ "$remote_main_before" != "$remote_main_after" ]; then
  printf '%s\n' 'release script changed the remote main branch' >&2
  exit 1
fi

printf '%s\n' 'release script contract passed'
