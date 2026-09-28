"""Block 6: SUSTAINED VOLUME. 3 rules live (flood/co_occurrence/threshold), steady
traffic for 5 minutes: every 15s one COMPLETE fresh group per rule (10 alerts/tick,
20 ticks = 200 alerts, ~0.7/s -- well under the ingest-drop threshold).
Watches: correctness per tick over time, active_windows_total growth, audit lag.
"""
import sys,os,time,collections,datetime
sys.path.insert(0,os.path.dirname(__file__)); import lib_probe as L
T=str(int(time.time()))[-6:]
F="SUS_F_%s"%T; A="SUS_A_%s"%T; B="SUS_B_%s"%T; TH="SUS_T_%s"%T
E=lambda n:{"alertname":n+"_SYN","labels":{"severity":"warning"}}
rules={}
for tag,pat,mc,en in (("flood","flood",{"match":{"alertname":F},"min_count":5},F),
                      ("cooc","co_occurrence",{"required_matches":[{"alertname":A},{"alertname":B}],"min_matches":2},A),
                      ("thr","threshold",{"match":{"alertname":TH},"distinct_label":"device","min_distinct_count":3},TH)):
    s,d=L.create_rule(name="sus-%s-%s"%(tag,T),pattern=pat,window_seconds=60,
        group_by_labels=["site"],match_criteria=mc,emit_spec=E(en),
        description="sustained probe run=sus-%s tag=%s"%(T,tag))
    rules[tag]=d["id"]; print(tag,s,d.get("id"),flush=True)
print("rules total:",L.rules_total()[0],flush=True)
time.sleep(4)
TICKS=20
def aw():
    s,d=L.call("GET","/api/v1/correlation_rules/debug/windows")
    if isinstance(d,dict):
        return d.get("active_windows_total") or d.get("total") or len(d.get("windows") or [])
    return "?"
print("active_windows before:",aw(),flush=True)
t_start=time.time()
for k in range(TICKS):
    g="sus%s-t%02d"%(T,k)
    for i in range(5): L.post_alert(F,{"site":g,"idx":str(i)})
    L.post_alert(A,{"site":g}); L.post_alert(B,{"site":g})
    for i in range(3): L.post_alert(TH,{"site":g,"device":"d%d"%i})
    if k%5==0: print(f"tick {k:02d} t+{time.time()-t_start:.0f}s active_windows={aw()}",flush=True)
    time.sleep(max(0,15-(time.time()-t_start-15*k)))
print("injection done, t+%.0fs; settling 90s"%(time.time()-t_start),flush=True)
time.sleep(90)
print("active_windows after settle:",aw(),flush=True)
def iso(s): return datetime.datetime.fromisoformat(s.replace("Z",""))
tot={}
for tag,name in (("flood",F),("cooc",A),("thr",TH)):
    rid=rules[tag]
    recs=L.history_for(name,pages=120)
    per=collections.defaultdict(collections.Counter); lags=[]
    for r in recs:
        for a in L.audit(r["am_fingerprint"]):
            if a.get("rule_id")==rid:
                per[r["labels"]["site"]][a["outcome"]]+=1
                lags.append((iso(a["created_at"])-iso(r["createdAt"])).total_seconds())
    fired=[k for k in per if per[k]["parent_emitted"]>=1]
    dbl=[k for k in per if per[k]["parent_emitted"]>1]
    lags.sort()
    early=[l for l in lags[:len(lags)//2]]; late=[l for l in lags[len(lags)//2:]]
    print(f"{tag}: history={len(recs)} groups={len(per)} fired={len(fired)}/{TICKS} double_fired={len(dbl)} "
          f"lag_min={min(lags) if lags else '-'} lag_med={lags[len(lags)//2] if lags else '-'} lag_max={max(lags) if lags else '-'}",flush=True)
    # degradation over time: fire rate first half vs second half of ticks
    fh=[k for k in fired if int(k[-2:])<TICKS//2]; sh=[k for k in fired if int(k[-2:])>=TICKS//2]
    print(f"   fire rate first half={len(fh)}/{TICKS//2}  second half={len(sh)}/{TICKS-TICKS//2}",flush=True)
for tag,i in rules.items(): L.del_rule(i)
print("active_windows after delete:",aw(),"rules total:",L.rules_total()[0],flush=True)
