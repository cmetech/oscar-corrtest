#!/bin/bash
# Deterministic reproducer: correlator event ordering.
# Usage: repro_seq.sh <mode> <trials>
#   mode=race    : A then B back-to-back, no startsAt  (expect flaky no-fire)
#   mode=spaced  : A, sleep 20, B, no startsAt         (expect always fire)
#   mode=stamped : A then B back-to-back, startsAt 60s apart (A earlier)
#   mode=inverted: B first (startsAt T+60), 20s later A (startsAt T) -> event-time in order,
#                  wall-clock inverted. Fires only if event time is honoured.
set -u
K="${OSCAR_API_KEY:?export OSCAR_API_KEY first}"
B=https://192.168.29.195/ext/mw
MODE=${1:-race}; N=${2:-6}
TAG=$(date +%s)
hex(){ head -c8 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
post(){ local an=$1 fp=$2 sa=${3:-}
  local extra=""; [ -n "$sa" ] && extra=",\"startsAt\":\"$sa\""
  curl -sk -o /dev/null -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
   -d "{\"receiver\":\"repro\",\"status\":\"firing\",\"groupKey\":\"$fp\",\"groupLabels\":{\"alertname\":\"$an\"},\"commonLabels\":{\"alertname\":\"$an\",\"severity\":\"critical\"},\"commonAnnotations\":{\"summary\":\"repro\"},\"alerts\":[{\"fingerprint\":\"$fp\",\"status\":\"firing\",\"labels\":{\"alertname\":\"$an\",\"severity\":\"critical\",\"site\":\"repro\"},\"annotations\":{\"summary\":\"repro\"}$extra}]}" \
   "$B/api/v1/alerts"; }
audit(){ curl -sk -H "X-API-Key: $K" "$B/api/v1/correlation_rules/audit?fingerprint=$1&page=1&perPage=20" | python3 /home/splunk/oscar_latest_all/oscar-corrtest/scripts/audit.py; }
FIRE=0
for t in $(seq 1 $N); do
  RID="${TAG}_${MODE}_$t"; A="REPROSEQ_${RID}_A"; Bn="REPROSEQ_${RID}_B"
  ID=$(curl -sk -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
    -d "{\"name\":\"reproseq-${RID,,}\",\"pattern\":\"sequence\",\"window_seconds\":300,\"group_by_labels\":[\"site\"],\"match_criteria\":{\"sequence\":[{\"alertname\":\"$A\"},{\"alertname\":\"$Bn\"}]},\"emit_spec\":{\"alertname\":\"REPROSEQ_SYN_${RID}\",\"labels\":{\"severity\":\"warning\"}},\"description\":\"repro run=$RID\"}" \
    "$B/api/v1/correlation_rules" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id","ERR"))')
  sleep 4
  FA=$(hex); FB=$(hex)
  T0=$(date -u -d '-120 seconds' +%Y-%m-%dT%H:%M:%SZ); T1=$(date -u -d '-60 seconds' +%Y-%m-%dT%H:%M:%SZ)
  case $MODE in
    race)    post "$A" "$FA"; post "$Bn" "$FB";;
    spaced)  post "$A" "$FA"; sleep 20; post "$Bn" "$FB";;
    stamped) post "$A" "$FA" "$T0"; post "$Bn" "$FB" "$T1";;
    inverted) post "$Bn" "$FB" "$T1"; sleep 20; post "$A" "$FA" "$T0";;
  esac
  sleep 25
  RA=$(audit $FA); RB=$(audit $FB)
  echo "$RA $RB" | grep -q parent_emitted && { FIRE=$((FIRE+1)); V=FIRED; } || V=no-fire
  echo "mode=$MODE trial=$t rule=$ID $V  A=$RA  B=$RB"
  curl -sk -o /dev/null -X DELETE -H "X-API-Key: $K" "$B/api/v1/correlation_rules/$ID"
done
echo "SUMMARY mode=$MODE fired=$FIRE/$N"
