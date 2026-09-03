"""D6 A/B: does window membership follow EVENT time or PROCESSING time?
Single variable = presence of startsAt. Two legs posted ~2s apart in real time.
ARM B legs carry startsAt 300s apart  -> event time says OUT of a 20s window -> must NOT fire.
ARM A legs carry no startsAt          -> correlator dates both at processing time -> fires.
"""
import sys,os,time,datetime,json
sys.path.insert(0,os.path.dirname(__file__)); import lib_probe as L

TOK=str(int(time.time()))[-6:]
def run(arm, use_starts_at):
    name="D6PROBE_%s_%s"%(arm,TOK)
    rule={"name":"d6-probe-%s-%s"%(arm.lower(),TOK),"pattern":"cross_source","window_seconds":20,
          "group_by_labels":["site"],
          "match_criteria":{"required_sources":[
              {"source":"snmp","match":{"alertname":name}},
              {"source":"api","match":{"alertname":name}}]},
          "emit_spec":{"alertname":name+"_SYN","labels":{"severity":"warning"}},
          "description":"D6 event-time probe run=d6probe-%s arm=%s"%(TOK,arm)}
    s,d=L.create_rule(**rule)
    rid=(d or {}).get("id") if isinstance(d,dict) else None
    print(f"[{arm}] create rule status={s} id={rid}")
    if rid is None: print(d); return
    time.sleep(3)
    base=datetime.datetime.now(datetime.timezone.utc)
    t1=(base-datetime.timedelta(seconds=300)).isoformat().replace("+00:00","Z")
    t2=base.isoformat().replace("+00:00","Z")
    print(f"[{arm}] leg1 ->",L.post_alert(name,{"site":"d6-"+arm,"oscar_source":"snmp"},
          starts_at=t1 if use_starts_at else None)[0], "startsAt",t1 if use_starts_at else None)
    time.sleep(2)
    print(f"[{arm}] leg2 ->",L.post_alert(name,{"site":"d6-"+arm,"oscar_source":"api"},
          starts_at=t2 if use_starts_at else None)[0], "startsAt",t2 if use_starts_at else None)
    time.sleep(45)
    recs=L.history_for(name)
    print(f"[{arm}] history rows={len(recs)}")
    outs=[]
    for r in recs:
        rows=L.audit(r["am_fingerprint"])
        for a in rows:
            if a.get("ruleId")==rid or a.get("rule_id")==rid:
                outs.append((r["labels"].get("oscar_source"),r["createdAt"],a.get("outcome"),a.get("createdAt") or a.get("created_at"),a.get("reason")))
    for o in sorted(outs,key=lambda x:str(x[3])): print("   ",o)
    fired=any(o[2]=="parent_emitted" for o in outs)
    print(f"[{arm}] RESULT fired={fired}  (expected {'False' if use_starts_at else 'True'})")
    print(f"[{arm}] delete rule ->",L.del_rule(rid)[0])
    return fired

fb=run("B",True)
time.sleep(5)
fa=run("A",False)
print("SUMMARY armB(startsAt 300s apart, posted 2s apart) fired=",fb," armA(no startsAt, posted 2s apart) fired=",fa)
print("rules total now:",L.rules_total()[0])
