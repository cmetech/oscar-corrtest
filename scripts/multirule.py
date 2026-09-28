"""Block 4: MANY RULES AT ONCE. 12 rules across 6 patterns live simultaneously,
including deliberate overlaps (two rules on the same alertname, a rule grouped on
an absent label) and three rules that must never fire. Checks cross-rule
interference, mis-attribution and audit rule_id keying."""
import sys,os,time,collections,json
sys.path.insert(0,os.path.dirname(__file__)); import lib_probe as L
T=str(int(time.time()))[-6:]
N=lambda s:"MR_%s_%s"%(s,T)
F,G,A,B,Z,TH,X,P,C,LG,Q=[N(x) for x in ("F","G","A","B","Z","TH","X","P","C","LG","Q")]
E=lambda n:{"alertname":n+"_SYN","labels":{"severity":"warning"}}
specs=[
 ("R01","flood",       120,["site"],{"match":{"alertname":F},"min_count":5}),
 ("R02","flood",       120,["site"],{"match":{"alertname":F},"min_count":5}),
 ("R03","co_occurrence",120,["site"],{"required_matches":[{"alertname":A},{"alertname":B}],"min_matches":2}),
 ("R04","threshold",   120,["site"],{"match":{"alertname":TH},"distinct_label":"device","min_distinct_count":3}),
 ("R05","cross_source",120,["site"],{"required_sources":[{"source":"snmp","match":{"alertname":X}},{"source":"api","match":{"alertname":X}}]}),
 ("R06","parent_child",120,["site"],{"parent_match":{"alertname":P},"child_matches":[{"alertname":C}],"suppress_children_for_notifiers":["email"],"tag_children_for_notifiers":[]}),
 ("R07","flood",       120,["site"],{"match":{"alertname":G},"min_count":5}),
 ("R08","co_occurrence",120,["site"],{"required_matches":[{"alertname":A},{"alertname":Z}],"min_matches":2}),
 ("R09","threshold",   120,["site"],{"match":{"alertname":TH},"distinct_label":"region","min_distinct_count":3}),
 ("R10","sequence",    120,["site"],{"sequence":[{"alertname":LG},{"alertname":Q}]}),
 ("R11","flood",       120,["device"],{"match":{"alertname":F},"min_count":5}),
 ("R12","parent_child",120,["zone"],{"parent_match":{"alertname":P},"child_matches":[{"alertname":C}],"suppress_children_for_notifiers":["email"],"tag_children_for_notifiers":[]}),
]
# alertnames each rule legitimately matches
own={"R01":{F},"R02":{F},"R03":{A,B},"R04":{TH},"R05":{X},"R06":{P,C},"R07":{G},
     "R08":{A,Z},"R09":{TH},"R10":{LG,Q},"R11":{F},"R12":{P,C}}
ids={}
for tag,pat,w,gb,mc in specs:
    s,d=L.create_rule(name="mr-%s-%s"%(tag.lower(),T),pattern=pat,window_seconds=w,
        group_by_labels=gb,match_criteria=mc,emit_spec=E(N(tag)),
        description="multirule probe run=mr-%s tag=%s"%(T,tag))
    if s!=201: print(tag,"CREATE FAIL",s,d); continue
    ids[tag]=d["id"]
print("rules:",ids)
print("total now:",L.rules_total()[0])
time.sleep(4)
site={"site":"mr-%s"%T}
posts=[]
for i in range(5): posts.append((F,dict(site,idx=str(i))))
posts += [(A,dict(site)),(B,dict(site))]
posts += [(TH,dict(site,device="d%d"%i)) for i in range(3)]
posts += [(X,dict(site,oscar_source="snmp")),(X,dict(site,oscar_source="api"))]
posts += [(P,dict(site))]
time.sleep(0)
for n,lb in posts: L.post_alert(n,lb)
time.sleep(3)
L.post_alert(C,dict(site,child_id="c1"))
L.post_alert(LG,dict(site))
time.sleep(3)
L.post_alert(Q,dict(site))
print("posted",len(posts)+3,"alerts")
time.sleep(60)
recs=L.history_for("MR_",pages=60)
recs=[r for r in recs if r["alertname"].endswith(T)]
print("history rows:",len(recs))
byrule=collections.defaultdict(collections.Counter)
mis=[]
rid2tag={v:k for k,v in ids.items()}
for r in recs:
    for a in L.audit(r["am_fingerprint"]):
        tag=rid2tag.get(a.get("rule_id"))
        if tag is None: continue
        byrule[tag][a["outcome"]]+=1
        if r["alertname"] not in own[tag]:
            mis.append((tag,a["rule_name"],r["alertname"],a["outcome"],a["reason"]))
for tag in sorted(ids):
    print(f"  {tag} id={ids[tag]:<4} {dict(byrule[tag])}")
print("MIS-ATTRIBUTION rows:",len(mis))
for m in mis[:15]: print("   ",m)
print("must-never-fire check: R07",dict(byrule['R07']),"R08",dict(byrule['R08']),"R09",dict(byrule['R09']))
for tag,i in ids.items(): L.del_rule(i)
print("rules total after cleanup:",L.rules_total()[0])
