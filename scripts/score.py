import json,glob,sys,os,collections
rows=collections.defaultdict(dict)
for d in sorted(glob.glob(sys.argv[1])):
    if any(x in d for x in ('windows','timers','rules.post')): continue
    try: j=json.load(open(d))
    except Exception: continue
    j=j.get('data',j); cr=j.get('canonicalReport',{})
    pat=cr.get('pattern')
    it=os.path.basename(os.path.dirname(d))
    for c in cr.get('cases',[]):
        rows[(pat,c['code'])][it]=c['verdict']
its=sorted({i for v in rows.values() for i in v}, key=lambda x:int(x.replace('run','')))
print(f"{'pattern.case':26s} "+" ".join(f"{i:7s}" for i in its)+"  NONPASS")
for k in sorted(rows):
    v=[rows[k].get(i,'-') for i in its]
    fails=sum(1 for x in v if x!='PASS')
    print(f"{k[0]+'.'+k[1]:26s} "+" ".join(f"{x[:7]:7s}" for x in v)+f"  {fails}/{len(v)}")
