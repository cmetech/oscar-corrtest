import sys,json
d=json.load(sys.stdin)
rows=d.get("rows",[])
print("|".join(r["outcome"]+"/"+str(r.get("reason"))[:60] for r in rows) or "NONE")
