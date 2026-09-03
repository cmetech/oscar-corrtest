"""Control for the threshold resolve arm: identical timing, NO resolve.
If distinct still fails to reach 3, the miss is D5 (tumbling HLL bucket) and the
resolve is innocent."""
import sys,os,time,collections;sys.path.insert(0,os.path.dirname(__file__));import lib_probe as L
T=str(int(time.time()))[-6:]; name="THRCTL_%s"%T; site={"site":"thrctl-"+T}
s,d=L.create_rule(name="thrctl-%s"%T,pattern="threshold",window_seconds=300,group_by_labels=["site"],
 match_criteria={"match":{"alertname":name},"distinct_label":"device","min_distinct_count":3},
 emit_spec={"alertname":name+"_SYN","labels":{"severity":"warning"}},
 description="threshold D5 control run=thrctl-%s"%T)
rid=d["id"];print("rule",rid,flush=True);time.sleep(3)
L.post_alert(name,dict(site,device="d1")); L.post_alert(name,dict(site,device="d2"))
print("d1,d2 posted; wait 160s (no resolve)",flush=True); time.sleep(160)
L.post_alert(name,dict(site,device="d3")); print("d3 posted",flush=True)
time.sleep(50)
rows=[]
for r in L.history_for(name,pages=60):
    for a in L.audit(r["am_fingerprint"]):
        if a.get("rule_id")==rid: rows.append((a["created_at"],r["labels"].get("device"),a["outcome"],a["reason"]))
for x in sorted(rows): print("  ",x)
print("parent_emitted =",sum(1 for x in rows if x[2]=="parent_emitted"))
print("del",L.del_rule(rid)[0],"total",L.rules_total()[0])
