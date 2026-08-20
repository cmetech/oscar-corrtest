#!/bin/sh
set -eu

for workflow in .github/workflows/ci.yml .github/workflows/release.yml; do
  grep -q 'make clean release-gate' "$workflow" || {
    printf 'workflow does not run the clean release gate: %s\n' "$workflow" >&2
    exit 1
  }
done

for asset_fragment in darwin_amd64.tar.gz darwin_arm64.tar.gz windows_amd64.zip; do
  grep -Fq "$asset_fragment" .github/workflows/ci.yml || {
    printf 'GitHub CI artifact set omits %s\n' "$asset_fragment" >&2
    exit 1
  }
done
grep -Fq 'dist/*.zip' .gitlab-ci.yml || {
  printf '%s\n' 'GitLab artifact/release lanes omit Windows ZIP assets' >&2
  exit 1
}
release_gate=$(sed -n '/^release-gate:/,/^$/p' Makefile)
for gate in installer-posix-check release-script-check package-content-check reproducible-check; do
  printf '%s\n' "$release_gate" | grep -q "$gate" || {
    printf 'offline release gate omits %s\n' "$gate" >&2
    exit 1
  }
done

release_workflow=.github/workflows/release.yml
for contract in \
  '^  verify:' \
  '^  windows-smoke:' \
  '^    runs-on: windows-2025$' \
  '^    needs: verify$' \
  '^  publish:' \
  '^    needs: \[verify, windows-smoke\]$' \
  'scripts/test-install-windows.ps1' \
  'actions/upload-artifact@[0-9a-f]\{40\}' \
  'actions/download-artifact@[0-9a-f]\{40\}'
do
  grep -q "$contract" "$release_workflow" || {
    printf 'release workflow is missing contract: %s\n' "$contract" >&2
    exit 1
  }
done
for asset in \
  linux_amd64.tar.gz \
  linux_arm64.tar.gz \
  darwin_amd64.tar.gz \
  darwin_arm64.tar.gz \
  windows_amd64.zip \
  SHA256SUMS
do
  count=$(grep -c "$asset" "$release_workflow" || true)
  if [ "$count" -lt 2 ]; then
    printf 'release workflow does not transfer and publish %s\n' "$asset" >&2
    exit 1
  fi
done

grep -q 'make clean release-gate' .gitlab-ci.yml || {
  printf '%s\n' 'GitLab verify job does not run the clean release gate' >&2
  exit 1
}
grep -q 'COPY --from=build --chown=65532:65532 /var/lib/oscar-corrtest /var/lib/oscar-corrtest' Containerfile || {
  printf '%s\n' 'scratch image does not contain an owned writable state directory' >&2
  exit 1
}
grep -q '^CMD \["help"\]$' Containerfile || {
  printf '%s\n' 'container default must be non-networking help' >&2
  exit 1
}

timer_output=$(go test -count=1 ./internal/scenario ./internal/compiler ./internal/runner -run 'TestDistributedJSONSchemaIsValidAndCoversEveryPattern|TestCompiledObservationWindowsCoverDecisionAndEvidenceLag|TestTimerStimulusScheduleIsDurableBeforeWaiting|TestRunnerExecutesEveryBuiltinPatternThroughOneCoordinator|TestPlanMaxDurationStopsInjectionAndRunsDetachedCleanup' 2>&1)
printf '%s\n' "$timer_output"
if printf '%s\n' "$timer_output" | grep -q '\[no tests to run\]'; then
  printf '%s\n' 'timer gate selected no tests in at least one package' >&2
  exit 1
fi

if sed -n '/^release-gate:/,/^$/p' Makefile | grep -q 'live-qualification'; then
  printf '%s\n' 'offline release gate must not depend on live qualification' >&2
  exit 1
fi
if (unset OSCAR_CORRTEST_LIVE_TARGET_ID OSCAR_CORRTEST_LIVE_PHASE_B_ACK OSCAR_CORRTEST_LIVE_DISPOSABLE_ACK; OSCAR_CORRTEST_BIN=/bin/false ./scripts/live-qualification.sh >/dev/null 2>&1); then
  printf '%s\n' 'live qualification did not fail closed before network access' >&2
  exit 1
fi
