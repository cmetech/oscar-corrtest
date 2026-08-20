#!/bin/sh
set -eu

required_ack='I_ACKNOWLEDGE_PHASE_B_ON_A_DISPOSABLE_TARGET'
target_id=${OSCAR_CORRTEST_LIVE_TARGET_ID:-}
phase_b_ack=${OSCAR_CORRTEST_LIVE_PHASE_B_ACK:-}
disposable_ack=${OSCAR_CORRTEST_LIVE_DISPOSABLE_ACK:-}

if [ -z "$target_id" ] || [ "$phase_b_ack" != "$required_ack" ] || [ "$disposable_ack" != "$required_ack" ]; then
  printf '%s\n' 'live qualification requires OSCAR_CORRTEST_LIVE_TARGET_ID and both Phase-B/disposable acknowledgements' >&2
  exit 2
fi

binary=${OSCAR_CORRTEST_BIN:-./bin/oscar-corrtest}
if [ ! -x "$binary" ]; then
  printf 'live qualification binary is not executable: %s\n' "$binary" >&2
  exit 2
fi

output_dir=${OSCAR_CORRTEST_LIVE_OUTPUT_DIR:-live-qualification-$(date -u +%Y%m%dT%H%M%SZ)}
if [ -e "$output_dir" ]; then
  printf 'live qualification output already exists: %s\n' "$output_dir" >&2
  exit 2
fi
mkdir -m 0700 "$output_dir"

data_args=
if [ -n "${OSCAR_CORRTEST_LIVE_DATA_DIR:-}" ]; then
  data_args="--data-dir ${OSCAR_CORRTEST_LIVE_DATA_DIR}"
fi

summary="$output_dir/qualification-summary.txt"
version=$($binary version)
{
  printf 'OSCAR corrtest live qualification\n'
  printf 'started_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'target_id=%s\n' "$target_id"
  printf 'pipeline_mode=phase_b_dispatch\n'
  printf 'harness=%s\n' "$version"
} > "$summary"

# shellcheck disable=SC2086 -- data_args is either empty or one fixed flag/value pair.
doctor=$($binary doctor --target "$target_id" --pipeline-mode phase_b_dispatch --output json $data_args)
printf '%s\n' "$doctor" > "$output_dir/doctor.json"
printf '%s' "$doctor" | grep -q '"compatible":true' || {
  printf '%s\n' 'doctor did not prove live compatibility' >&2
  exit 1
}

patterns='co_occurrence flood sequence persistence absence parent_child cross_source threshold'
for pattern in $patterns; do
  run_json="$output_dir/run-$pattern.json"
  # shellcheck disable=SC2086 -- data_args is either empty or one fixed flag/value pair.
  if ! $binary run "builtin:$pattern" --target "$target_id" --pipeline-mode phase_b_dispatch --output json $data_args > "$run_json"; then
    printf 'pattern=%s result=COMMAND_ERROR\n' "$pattern" >> "$summary"
    exit 1
  fi
  grep -q '"verdict":"PASS"' "$run_json" || {
    printf 'pattern=%s result=NON_PASS\n' "$pattern" >> "$summary"
    exit 1
  }
  grep -q '"cleanupStatus":"CLEAN"' "$run_json" || {
    printf 'pattern=%s result=CLEANUP_NOT_CLEAN\n' "$pattern" >> "$summary"
    exit 1
  }
  run_id=$(sed -n 's/^{"id":"\([^"]*\)".*/\1/p' "$run_json")
  if [ -z "$run_id" ]; then
    printf 'pattern=%s result=MISSING_RUN_ID\n' "$pattern" >> "$summary"
    exit 1
  fi
  bundle="$output_dir/$pattern-$run_id.zip"
  # shellcheck disable=SC2086 -- data_args is either empty or one fixed flag/value pair.
  $binary export --output "$bundle" $data_args "$run_id" > "$output_dir/export-$pattern.txt"
  # shellcheck disable=SC2086 -- data_args is either empty or one fixed flag/value pair.
  $binary verify-bundle $data_args "$bundle" > "$output_dir/verify-$pattern.txt"
  printf 'pattern=%s run_id=%s verdict=PASS cleanup=CLEAN bundle=VERIFIED\n' "$pattern" "$run_id" >> "$summary"
done

printf 'completed_at=%s\noverall=PASS\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$summary"
printf 'Live qualification passed; summary: %s\n' "$summary"
