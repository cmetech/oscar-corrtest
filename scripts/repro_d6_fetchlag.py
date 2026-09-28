"""D6 root-cause reproducer: correlator pull-fetch is batch=10/timeout=30
(consume.py:652). On a quiet box a LONE alert waits for the 30s fetch expiry
before it is evaluated; a burst of >=10 alerts is evaluated immediately.
Measured as alert-accepted -> CorrelationAudit row lag.
ARM 1: 1 alert   -> expect lag ~= 30s
ARM 2: 10 alerts -> expect lag ~= 0-2s
"""
import sys,os,time,datetime
sys.path.insert(0,os.path.dirname(__file__)); import lib_probe as L
TOK=str(int(time.time()))[-6:]
def iso(s): return datetime.datetime.fromisoformat(s.replace("Z","")).timestamp()
def arm(label,n):
    name="D6LAG_%s_%s"%(label,TOK)
    s,d=L.create_rule(name="d6lag-%s-%s"%(label.lower(),TOK),pattern="flood",window_seconds=120,
        group_by_labels=["site"],match_criteria={"match":{"alertname":name},"min_count":999},
        emit_spec={"alertname":name+"_SYN","labels":{"severity":"warning"}},
        description="D6 fetch-lag probe run=d6lag-%s arm=%s"%(TOK,label))
    rid=d.get("id"); print(f"[{label}] rule {rid} status={s}")
    time.sleep(3)
    t0=time.time()
    for i in range(n):
        L.post_alert(name,{"site":"d6lag-"+label,"idx":str(i)})
    print(f"[{label}] posted {n} alerts at t0")
    time.sleep(50)
    recs=L.history_for(name,pages=15)
    lags=[]
    for r in recs:
        for a in L.audit(r["am_fingerprint"]):
            if a.get("ruleId")==rid:
                lags.append(iso(a["createdAt"])-t0)
    lags.sort()
    print(f"[{label}] n_alerts={len(recs)} n_audits={len(lags)} lags(s)={[round(x,1) for x in lags]}")
    print(f"[{label}] delete ->",L.del_rule(rid)[0])
arm("SINGLE",1)
time.sleep(5)
arm("BURST10",10)
print("rules total:",L.rules_total()[0])
