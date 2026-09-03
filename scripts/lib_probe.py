import json,os,ssl,time,urllib.request,hashlib
B=os.environ.get("B","https://192.168.29.195/ext/mw")
K=os.environ["OSCAR_API_KEY"]
CTX=ssl.create_default_context(); CTX.check_hostname=False; CTX.verify_mode=ssl.CERT_NONE
def call(method,path,body=None):
    data=json.dumps(body).encode() if body is not None else None
    r=urllib.request.Request(B+path,data=data,method=method,
        headers={"X-API-Key":K,"Content-Type":"application/json"})
    try:
        with urllib.request.urlopen(r,context=CTX,timeout=60) as f:
            t=f.read().decode()
            return f.status,(json.loads(t) if t.strip() else None)
    except urllib.error.HTTPError as e:
        t=e.read().decode()
        try: return e.code,json.loads(t)
        except Exception: return e.code,t
def am_fp(labels):
    # alertmanager fingerprint: sha of sorted label pairs -> harness uses same; we read from history instead
    return None
def create_rule(**kw):
    body={"priority":100,"max_synthetic_per_minute":10,"enabled":True,
          "created_by":"corrtest-probe"}
    body.update(kw)
    return call("POST","/api/v1/correlation_rules",body)
def del_rule(i): return call("DELETE","/api/v1/correlation_rules/%d"%i)
def rules_total():
    s,d=call("GET","/api/v1/correlation_rules?page=1&per_page=100"); return d["total"],d["rows"]
def post_alert(name,labels,status="firing",starts_at=None,annotations=None):
    lb=dict(labels); lb["alertname"]=name
    ann=annotations or {"summary":"probe "+name}
    a={"fingerprint":hashlib.sha256(json.dumps(sorted(lb.items())).encode()).hexdigest()[:16],
       "status":status,"labels":lb,"annotations":ann}
    if starts_at: a["startsAt"]=starts_at
    g={"receiver":"oscar-corrtest","status":status,"groupKey":"probe:"+name,
       "groupLabels":{"alertname":name},"commonLabels":lb,"commonAnnotations":ann,"alerts":[a]}
    if starts_at: g["alerts"][0]["startsAt"]=starts_at
    return call("POST","/api/v1/alerts",g)
def history_for(prefix,pages=12):
    out=[]
    for p in range(1,pages+1):
        s,d=call("GET","/api/v1/alerts/history?page=%d&size=10"%p)
        if not d or not d.get("records"): break
        for r in d["records"]:
            if r.get("alertname","").startswith(prefix):
                lab={x["Label"]:x["Value"] for x in (r.get("labels") or [])}
                out.append({"alertname":r["alertname"],"createdAt":r["createdAt"],
                            "am_fingerprint":lab.get("am_fingerprint"),"status":r.get("status"),"labels":lab})
        if p>=d.get("total_pages",1): break
    return out
def audit(fp):
    s,d=call("GET","/api/v1/correlation_rules/audit?fingerprint=%s&page=1&perPage=50"%fp)
    if isinstance(d,dict): return d.get("rows") or d.get("records") or []
    return []
