"""Block 5: does a mid-window RESOLVE disturb a counting pattern?
Source claim (patterns/*.py is_resolved -> return None) says flood, co_occurrence,
threshold ignore resolves entirely. Verified empirically with the resolve placed
>2min after its firing so JetStream's fingerprint dedup (D1) cannot swallow it —
the resolve genuinely reaches the correlator.
Expected: the resolve produces NO audit row and does NOT reduce/advance the count;
the window still completes on the last firing."""
import sys,os,time,collections
sys.path.insert(0,os.path.dirname(__file__)); import lib_probe as L
T=str(int(time.time()))[-6:]
E=lambda n:{"alertname":n+"_SYN","labels":{"severity":"warning"}}
def show(name,rid,note):
    recs=[r for r in L.history_for(name,pages=60)]
    rows=[]
    for r in recs:
        for a in L.audit(r["am_fingerprint"]):
            if a.get("rule_id")==rid:
                rows.append((a["created_at"],r["status"],a["outcome"],a["reason"]))
    for x in sorted(rows): print("    ",x)
    print("    history statuses:",collections.Counter(r["status"] for r in recs))
    print("    RESULT",note,"parent_emitted=",sum(1 for x in rows if x[2]=="parent_emitted"))

def flood():
    name="RES_FLOOD_%s"%T; site={"site":"resf-"+T}
    s,d=L.create_rule(name="res-flood-%s"%T,pattern="flood",window_seconds=300,group_by_labels=["site"],
      match_criteria={"match":{"alertname":name},"min_count":5},emit_spec=E(name),
      description="resolve-path probe run=res-%s"%T)
    rid=d["id"];print("flood rule",rid);time.sleep(3)
    for i in range(4): L.post_alert(name,dict(site,idx=str(i)))
    print("   4 firings posted; waiting 150s so the resolve escapes the D1 dedup window")
    time.sleep(150)
    print("   resolve idx0 ->",L.post_alert(name,dict(site,idx="0"),status="resolved")[0])
    time.sleep(10)
    print("   5th firing ->",L.post_alert(name,dict(site,idx="4"))[0])
    time.sleep(45); show(name,rid,"flood (expect count reaches 5 and fires once)")
    L.del_rule(rid)

def cooc():
    name="RES_COOC_%s"%T; site={"site":"resc-"+T}
    A,B=name+"_A",name+"_B"
    s,d=L.create_rule(name="res-cooc-%s"%T,pattern="co_occurrence",window_seconds=300,group_by_labels=["site"],
      match_criteria={"required_matches":[{"alertname":A},{"alertname":B}],"min_matches":2},
      emit_spec=E(name),description="resolve-path probe run=res-%s"%T)
    rid=d["id"];print("cooc rule",rid);time.sleep(3)
    L.post_alert(A,dict(site))
    print("   A firing posted; waiting 150s"); time.sleep(150)
    print("   resolve A ->",L.post_alert(A,dict(site),status="resolved")[0])
    time.sleep(10)
    print("   B firing ->",L.post_alert(B,dict(site))[0])
    time.sleep(45); show(name,rid,"co_occurrence (expect fire: resolve must not remove A)")
    L.del_rule(rid)

def thr():
    name="RES_THR_%s"%T; site={"site":"rest-"+T}
    s,d=L.create_rule(name="res-thr-%s"%T,pattern="threshold",window_seconds=300,group_by_labels=["site"],
      match_criteria={"match":{"alertname":name},"distinct_label":"device","min_distinct_count":3},
      emit_spec=E(name),description="resolve-path probe run=res-%s"%T)
    rid=d["id"];print("threshold rule",rid);time.sleep(3)
    L.post_alert(name,dict(site,device="d1")); L.post_alert(name,dict(site,device="d2"))
    print("   d1,d2 posted; waiting 150s"); time.sleep(150)
    print("   resolve d1 ->",L.post_alert(name,dict(site,device="d1"),status="resolved")[0])
    time.sleep(10)
    print("   d3 firing ->",L.post_alert(name,dict(site,device="d3"))[0])
    time.sleep(45); show(name,rid,"threshold (expect distinct reaches 3 and fires)")
    L.del_rule(rid)

flood(); cooc(); thr()
print("rules total:",L.rules_total()[0])
