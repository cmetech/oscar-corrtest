import json,sys
for f in sys.argv[1:]:
    try:
        d=json.load(open(f))
    except Exception as e:
        print(f"{f:52s} PARSE-ERR {e}"); continue
    d=d.get('data',d); cr=d.get('canonicalReport',{})
    rows=[f"{c.get('code')}={c.get('verdict')}" for c in cr.get('cases',[])]
    ao=[]
    for c in cr.get('cases',[]):
        ao += (c.get('auditOutcomes') or [])
    print(f"{f:52s} {str(cr.get('verdict','?')):13s} {str(d.get('cleanupStatus','?')):8s} {' '.join(rows):26s} {','.join(sorted(set(ao)))}")
