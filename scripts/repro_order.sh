#!/bin/bash
# Deterministic reproducer for the parent_child / sequence ordering race.
# Usage: repro_order.sh <pattern: pc|seq> <gap_seconds> <trials>
set -u
K="${OSCAR_API_KEY:?export OSCAR_API_KEY first}"
B=https://192.168.29.195/ext/mw
PAT=${1:-pc}; GAP=${2:-0}; N=${3:-6}
TAG=$(date +%s)
hex(){ head -c8 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
post(){ # $1 alertname $2 fingerprint [$3 startsAt]
  local extra=""
  [ $# -ge 3 ] && extra=",\"startsAt\":\"$3\""
  curl -sk -o /dev/null -w '%{http_code} ' -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
   -d "{\"receiver\":\"repro\",\"status\":\"firing\",\"groupKey\":\"$2\",\"groupLabels\":{\"alertname\":\"$1\"},\"commonLabels\":{\"alertname\":\"$1\",\"severity\":\"critical\"},\"commonAnnotations\":{\"summary\":\"repro\"},\"alerts\":[{\"fingerprint\":\"$2\",\"status\":\"firing\",\"labels\":{\"alertname\":\"$1\",\"severity\":\"critical\",\"site\":\"repro\"},\"annotations\":{\"summary\":\"repro\"}$extra}]}" \
   "$B/api/v1/alerts"
}
audit(){ curl -sk -H "X-API-Key: $K" "$B/api/v1/correlation_rules/audit?fingerprint=$1&page=1&perPage=20" | python3 /home/splunk/oscar_latest_all/oscar-corrtest/scripts/audit.py; }

for t in $(seq 1 $N); do
  RID="R${TAG}_$PAT${GAP}_$t"
  A="REPRO_${RID}_A"; Bn="REPRO_${RID}_B"
  if [ "$PAT" = pc ]; then
    MC="{\"parent_match\":{\"alertname\":\"$A\"},\"child_matches\":[{\"alertname\":\"$Bn\"}],\"suppress_children_for_notifiers\":[\"email\"],\"tag_children_for_notifiers\":[]}"
    P=parent_child
  else
    MC="{\"sequence\":[{\"alertname\":\"$A\"},{\"alertname\":\"$Bn\"}]}"
    P=sequence
  fi
  RULE=$(curl -sk -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
    -d "{\"name\":\"repro-${RID,,}\",\"pattern\":\"$P\",\"window_seconds\":120,\"group_by_labels\":[\"site\"],\"match_criteria\":$MC,\"emit_spec\":{\"alertname\":\"REPRO_SYN_${RID}\",\"labels\":{\"severity\":\"warning\"}},\"description\":\"repro run=$RID\"}" \
    "$B/api/v1/correlation_rules")
  RID_NUM=$(echo "$RULE" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id","ERR"))' 2>/dev/null)
  sleep 3
  FA=$(hex); FB=$(hex)
  post "$A" "$FA"; [ "$GAP" != 0 ] && sleep $GAP
  post "$Bn" "$FB"; echo -n " "
  sleep 25
  echo "trial=$t rule=$RID_NUM gap=$GAP A[$FA]=$(audit $FA) B[$FB]=$(audit $FB)"
  curl -sk -o /dev/null -X DELETE -H "X-API-Key: $K" "$B/api/v1/correlation_rules/$RID_NUM"
done
