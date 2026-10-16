import hashlib,json,os,pathlib,subprocess,time,uuid
C=pathlib.Path('/Users/coreyellis/code/github.com/cmetech/oscar_app/.worktrees/correlation-reliability-20260919/oscar-corrtest')
R=C.parent/'oscar'
folder=C/'docs/qualification/20261015/persistence-timer'
manifest=folder/'actual-1-owner.json'
assert not manifest.exists()
name='oscar-corrtest-persistence-'+uuid.uuid4().hex[:12]
def command(*args):
 return subprocess.run(['docker',*args],capture_output=True,text=True,check=True,timeout=30).stdout.strip()
image=command('image','inspect','redis:7-alpine','--format','{{.Id}}')
runner=folder/'run.py'
before=hashlib.sha256(runner.read_bytes()).hexdigest()
data=dict(name=name,image=image,runner_before=before,source_sha256='08fc0f78b805d14d31333197473db818912339c001957b5c1178689f49c38a8d')
manifest.write_text(json.dumps(data,indent=2)+'\n')
container=None
try:
 container=command('run','-d','--pull=never','--name',name,'--label','oscar.corrtest-persistence='+name,'--user','1000:1000','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/data:rw,mode=1777','--tmpfs','/tmp:rw,mode=1777','-p','127.0.0.1::6379','--entrypoint','redis-server',image,'--save','','--appendonly','no','--bind','0.0.0.0')
 data['container']=container
 info=json.loads(command('inspect',container))[0]
 assert info['Config']['Labels']['oscar.corrtest-persistence']==name
 assert not any(m['Type']=='volume' for m in info['Mounts'])
 port=info['NetworkSettings']['Ports']['6379/tcp'][0]
 assert port['HostIp']=='127.0.0.1'
 import redis
 client=redis.Redis(host='127.0.0.1',port=int(port['HostPort']),socket_timeout=2)
 for _ in range(30):
  try:
   client.ping(); break
  except redis.ConnectionError:time.sleep(.1)
 server=client.info('server');client.close()
 data.update(redis_run_id=server['run_id'],port=port['HostPort'])
 manifest.write_text(json.dumps(data,indent=2)+'\n')
 args=['/Users/coreyellis/.pyenv/versions/3.13.13/bin/python3.13','-B',str(runner),'--source',str(R/'oscar-correlator/src'),'--source-sha256',data['source_sha256'],'--isolated-redis-run-id',data['redis_run_id'],'--output',str(folder/'actual-1')]
 data['command']=args
 with (folder/'actual-1-run.log').open('x') as output:
  result=subprocess.run(args,cwd=C,env={**os.environ,'PYTHONDONTWRITEBYTECODE':'1','USE_VAULT':'false','CORRTEST_PERSISTENCE_REDIS_URL':'redis://127.0.0.1:'+port['HostPort']+'/0'},stdout=output,stderr=subprocess.STDOUT,timeout=80)
 data['returncode']=result.returncode
finally:
 if container:
  info=json.loads(command('inspect',container))[0]
  assert info['Config']['Labels']['oscar.corrtest-persistence']==name
  command('rm','-f',container)
  absent=subprocess.run(['docker','inspect',container],capture_output=True,text=True,timeout=30)
  assert absent.returncode!=0 and 'no such object' in absent.stderr.lower() and container in absent.stderr
  data['container_absent']=True
 data['runner_after']=hashlib.sha256(runner.read_bytes()).hexdigest()
 data['runner_parity']=data['runner_after']==before
 manifest.write_text(json.dumps(data,indent=2)+'\n')
print(json.dumps(data))
assert data['returncode']==0 and data['runner_parity'] and data['container_absent']
