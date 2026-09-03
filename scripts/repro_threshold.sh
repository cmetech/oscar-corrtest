#!/bin/bash
# Deterministic reproducer: threshold HLL tumbling-bucket boundary defect.
# Usage: repro_threshold.sh <straddle|inside> <trials>
set -u
K="${OSCAR_API_KEY:?export OSCAR_API_KEY first}"
B=https://192.168.29.195/ext/mw
MODE=${1:-straddle}; N=${2:-3}
hex(){ head -c8 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
post(){ curl -sk -o /dev/null -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
 -d "{\"receiver\":\"repro\",\"status\":\"firing\",\"groupKey\":\"$2\",\"groupLabels\":{\"alertname\":\"$1\"},\"commonLabels\":{\"alertname\":\"$1\",\"severity\":\"critical\"},\"commonAnnotations\":{\"summary\":\"r\"},\"alerts\":[{\"fingerprint\":\"$2\",\"status\":\"firing\",\"labels\":{\"alertname\":\"$1\",\"severity\":\"critical\",\"site\":\"repro\",\"device\":\"$3\"},\"annotations\":{\"summary\":\"r\"}}]}" \
 "$B/api/v1/alerts"; }
audit(){ curl -sk -H "X-API-Key: $K" "$B/api/v1/correlation_rules/audit?fingerprint=$1&page=1&perPage=20" | python3 /home/splunk/oscar_latest_all/oscar-corrtest/scripts/audit.py; }
FIRE=0
for t in $(seq 1 $N); do
  RID="$(date +%s)_${MODE}_$t"; A="REPROTH_${RID}"
  ID=$(curl -sk -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
   -d "{\"name\":\"reproth-${RID,,}\",\"pattern\":\"threshold\",\"window_seconds\":30,\"group_by_labels\":[\"site\"],\"match_criteria\":{\"match\":{\"alertname\":\"$A\"},\"distinct_label\":\"device\",\"min_distinct_count\":3},\"emit_spec\":{\"alertname\":\"REPROTH_SYN_${RID}\",\"labels\":{\"severity\":\"warning\"}},\"description\":\"repro run=$RID\"}" \
   "$B/api/v1/correlation_rules" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id","ERR"))')
  sleep 4
  # Correlator ts = processing wall-clock (~2s after POST). Aim the MIDDLE of the
  # 3-alert burst at the 30s bucket boundary (straddle) or at bucket+12s (inside).
  TARGET=$( [ "$MODE" = straddle ] && echo 0 || echo 12 )
  python3 -c "
import time
while True:
    p=(time.time()*1000)%30000/1000.0
    # POST now -> correlator sees it ~2s later; want that landing at TARGET
    want=($TARGET-2)%30
    if abs(p-want)<0.15: break
    time.sleep(0.02)"
  F1=$(hex); F2=$(hex); F3=$(hex)
  post "$A" "$F1" d1; post "$A" "$F2" d2; post "$A" "$F3" d3
  sleep 25
  R="$(audit $F1) $(audit $F2) $(audit $F3)"
  echo "$R" | grep -q parent_emitted && { FIRE=$((FIRE+1)); V=FIRED; } || V=no-fire
  echo "mode=$MODE trial=$t rule=$ID $V :: $R"
  curl -sk -o /dev/null -X DELETE -H "X-API-Key: $K" "$B/api/v1/correlation_rules/$ID"
done
echo "SUMMARY threshold mode=$MODE fired=$FIRE/$N"
