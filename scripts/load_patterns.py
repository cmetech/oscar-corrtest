"""Block 3: LOAD for non-sequence patterns. 20 concurrent groups, and within each
group every leg is fired SIMULTANEOUSLY (thread pool) so the within-group race is
actually exercised. Windows are 120s so D6 lag jitter cannot confound the result.
"""
import sys,os,time,json,threading,collections
from concurrent.futures import ThreadPoolExecutor
sys.path.insert(0,os.path.dirname(__file__)); import lib_probe as L

G=int(os.environ.get("GROUPS","20"))
TOK=str(int(time.time()))[-6:]

def spec(p,name):
    if p=="flood":
        return {"match":{"alertname":name},"min_count":5}, 5
    if p=="co_occurrence":
        return {"required_matches":[{"alertname":name+"_A"},{"alertname":name+"_B"}],"min_matches":2}, 2
    if p=="threshold":
        return {"match":{"alertname":name},"distinct_label":"device","min_distinct_count":3}, 3
    if p=="cross_source":
        return {"required_sources":[{"source":"snmp","match":{"alertname":name}},
                                    {"source":"api","match":{"alertname":name}}]}, 2
    if p=="parent_child":
        return {"parent_match":{"alertname":name+"_P"},
                "child_matches":[{"alertname":name+"_C"}],
                "suppress_children_for_notifiers":["email"],
                "tag_children_for_notifiers":[]}, 2
    raise SystemExit(p)

def legs(p,name,g):
    s="ld%s-g%02d"%(TOK,g)
    if p=="flood":        return [(name,{"site":s,"idx":str(i)}) for i in range(5)]
    if p=="co_occurrence":return [(name+"_A",{"site":s}),(name+"_B",{"site":s})]
    if p=="threshold":    return [(name,{"site":s,"device":"d%d"%i}) for i in range(3)]
    if p=="cross_source": return [(name,{"site":s,"oscar_source":"snmp"}),(name,{"site":s,"oscar_source":"api"})]
    if p=="parent_child": return [(name+"_P",{"site":s}),(name+"_C",{"site":s,"child_id":"c1"})]

def run(p):
    name="LOAD_%s_%s"%(p.upper(),TOK)
    mc,_=spec(p,name)
    body=dict(name="load-%s-%s"%(p,TOK),pattern=p,window_seconds=120,group_by_labels=["site"],
        match_criteria=mc,emit_spec={"alertname":name+"_SYN","labels":{"severity":"warning"}},
        description="load probe run=load-%s pattern=%s"%(TOK,p))
    s,d=L.create_rule(**body)
    if s!=201: print(p,"CREATE FAILED",s,d); return
    rid=d["id"]; print(f"== {p} rule={rid} groups={G}")
    time.sleep(3)
    jobs=[]
    for g in range(G):
        for n,lb in legs(p,name,g): jobs.append((n,lb))
    t0=time.time()
    with ThreadPoolExecutor(max_workers=64) as ex:
        res=list(ex.map(lambda a: L.post_alert(a[0],a[1])[0], jobs))
    print(f"   posted {len(jobs)} alerts in {time.time()-t0:.1f}s, non200={sum(1 for r in res if r!=200)}")
    time.sleep(60)
    recs=L.history_for(name,pages=40)
    print(f"   history rows matching prefix: {len(recs)} (expected {len(jobs)})")
    per=collections.defaultdict(collections.Counter)
    nau=0
    for r in recs:
        for a in L.audit(r["am_fingerprint"]):
            if a.get("rule_id")==rid:
                nau+=1; per[r["labels"].get("site")][a["outcome"]]+=1
    print(f"   audit rows for rule: {nau}")
    key="suppressed_per_notifier" if p=="parent_child" else "parent_emitted"
    counts=collections.Counter(per[k][key] for k in per)
    print(f"   groups seen={len(per)}  {key} per group -> {dict(counts)}")
    bad={k:dict(v) for k,v in per.items() if v[key]!=1}
    if bad: print("   ANOMALOUS GROUPS:",json.dumps(bad)[:1200])
    s,d=L.call("GET","/api/v1/correlation_rules/debug/windows")
    try: print("   active_windows_total:",(d or {}).get("active_windows_total", (d or {}).get("total")))
    except Exception: pass
    print("   delete ->",L.del_rule(rid)[0])
    return len(per),dict(counts)

for p in (sys.argv[1:] or ["flood","co_occurrence","threshold","cross_source","parent_child"]):
    run(p); time.sleep(8)
print("rules total:",L.rules_total()[0])
