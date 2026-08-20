#!/bin/sh
set -eu

for workflow in .github/workflows/ci.yml .github/workflows/release.yml; do
  grep -q 'make clean release-gate' "$workflow" || {
    printf 'workflow does not run the clean release gate: %s\n' "$workflow" >&2
    exit 1
  }
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
