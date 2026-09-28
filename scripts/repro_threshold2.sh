#!/bin/bash
# DETERMINISTIC reproducer: threshold HLL tumbling-bucket boundary defect.
# Uses explicit startsAt so the correlator's event ts is fully controlled.
#   straddle : d1,d2 just BEFORE a 30s bucket boundary, d3 just AFTER  -> must NOT fire (defect)
#   inside   : d1,d2,d3 all inside one bucket, same 3s spread          -> fires
set -u
K="${OSCAR_API_KEY:?export OSCAR_API_KEY first}"; B=https://192.168.29.195/ext/mw
MODE=${1:-straddle}; N=${2:-3}
hex(){ head -c8 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
post(){ curl -sk -o /dev/null -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
 -d "{\"receiver\":\"repro\",\"status\":\"firing\",\"groupKey\":\"$2\",\"groupLabels\":{\"alertname\":\"$1\"},\"commonLabels\":{\"alertname\":\"$1\",\"severity\":\"critical\"},\"commonAnnotations\":{\"summary\":\"r\"},\"alerts\":[{\"fingerprint\":\"$2\",\"status\":\"firing\",\"startsAt\":\"$4\",\"labels\":{\"alertname\":\"$1\",\"severity\":\"critical\",\"site\":\"repro\",\"device\":\"$3\"},\"annotations\":{\"summary\":\"r\"}}]}" \
 "$B/api/v1/alerts"; }
audit(){ curl -sk -H "X-API-Key: $K" "$B/api/v1/correlation_rules/audit?fingerprint=$1&page=1&perPage=20" | python3 /home/splunk/oscar_latest_all/oscar-corrtest/scripts/audit.py; }
FIRE=0
for t in $(seq 1 $N); do
  RID="$(date +%s)_${MODE}_$t"; A="REPROTH2_${RID}"
  ID=$(curl -sk -X POST -H "X-API-Key: $K" -H 'Content-Type: application/json' \
   -d "{\"name\":\"reproth2-${RID,,}\",\"pattern\":\"threshold\",\"window_seconds\":30,\"group_by_labels\":[\"site\"],\"match_criteria\":{\"match\":{\"alertname\":\"$A\"},\"distinct_label\":\"device\",\"min_distinct_count\":3},\"emit_spec\":{\"alertname\":\"REPROTH2_SYN_${RID}\",\"labels\":{\"severity\":\"warning\"}},\"description\":\"repro run=$RID\"}" \
   "$B/api/v1/correlation_rules" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("id","ERR"))')
  sleep 4
  read T1 T2 T3 <<<$(python3 -c "
import time,datetime
b=(int(time.time()*1000)-60000)//30000*30000
o=[-2000,-1000,1000] if '$MODE'=='straddle' else [1000,2000,3000]
print(' '.join(datetime.datetime.utcfromtimestamp((b+x)/1000).strftime('%Y-%m-%dT%H:%M:%S.%f')[:-3]+'Z' for x in o))")
  F1=$(hex); F2=$(hex); F3=$(hex)
  post "$A" "$F1" d1 "$T1"; post "$A" "$F2" d2 "$T2"; post "$A" "$F3" d3 "$T3"
  sleep 25
  R="$(audit $F1) ;; $(audit $F2) ;; $(audit $F3)"
  echo "$R" | grep -q parent_emitted && { FIRE=$((FIRE+1)); V=FIRED; } || V=NO-FIRE
  echo "mode=$MODE trial=$t rule=$ID ts=[$T1 $T2 $T3] $V :: $R"
  curl -sk -o /dev/null -X DELETE -H "X-API-Key: $K" "$B/api/v1/correlation_rules/$ID"
done
echo "SUMMARY threshold2 mode=$MODE fired=$FIRE/$N"
