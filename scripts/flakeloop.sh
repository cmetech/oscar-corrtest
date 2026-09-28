#!/bin/bash
export OSCAR_API_KEY="${OSCAR_API_KEY:?export OSCAR_API_KEY first}"
B=https://192.168.29.195/ext/mw
CT=/home/splunk/oscar_latest_all/oscar-corrtest/bin/oscar-corrtest
TGT=tgt_6KYCH7ESVS65T87RNBPQ97SSFK
cd /home/splunk/oscar_latest_all/oscar-corrtest
N=${1:-6}
START=${2:-1}
for i in $(seq $START $((START+N-1))); do
  d=flake/run$i; mkdir -p $d
  curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules/debug/windows" > $d/windows.pre.json
  for p in flood co_occurrence sequence persistence absence parent_child cross_source threshold; do
    ts=$(date -u +%Y-%m-%dT%H:%M:%S)
    $CT run builtin:$p --target $TGT --pipeline-mode phase_a_audit_only --output json > $d/$p.json 2> $d/$p.log
    echo "$p exit=$? start=$ts end=$(date -u +%Y-%m-%dT%H:%M:%S)" >> $d/times.txt
    curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules/debug/windows" > $d/windows.after_$p.json
  done
  curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules/debug/timers" > $d/timers.post.json
  curl -sk -H "X-API-Key: $OSCAR_API_KEY" "$B/api/v1/correlation_rules?page=1&per_page=50" > $d/rules.post.json
  echo "ITER $i done $(date -u)" >> flake/progress.txt
done
echo ALLDONE >> flake/progress.txt
