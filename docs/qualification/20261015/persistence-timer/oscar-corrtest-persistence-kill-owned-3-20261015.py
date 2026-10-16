import hashlib,json,pathlib,subprocess,time,uuid
C=pathlib.Path('/Users/coreyellis/code/github.com/cmetech/oscar_app/.worktrees/correlation-reliability-20260919/oscar-corrtest'); R=C.parent/'oscar'
folder=C/'docs/qualification/20261015/persistence-timer';manifest=folder/'kill-3-owner.json';assert not manifest.exists()
name='oscar-corrtest-persistence-'+uuid.uuid4().hex[:12]
def command(*args):return subprocess.run(['docker',*args],capture_output=True,text=True,check=True,timeout=30).stdout.strip()
def digest(path):return hashlib.sha256(path.read_bytes()).hexdigest()
image=command('image','inspect','redis:7-alpine','--format','{{.Id}}');controller='sha256:e3a34f7d4a024c6f991a8a6477770ab0a5ae2ad809f80d544e78e908ff63d4c3'
assert command('image','inspect',controller,'--format','{{.Id}}')==controller
before={n:digest(folder/n) for n in ['run.py','resume.py']}
data=dict(name=name,image=image,controller_image=controller,fixture_before=before,source_sha256='08fc0f78b805d14d31333197473db818912339c001957b5c1178689f49c38a8d',owned_containers=[],removed=[],commands=[])
def save():manifest.write_text(json.dumps(data,indent=2)+'\n')
save()
try:
 redis_id=command('run','-d','--pull=never','--name',name,'--label','oscar.corrtest-persistence='+name,'--user','1000:1000','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/data:rw,mode=1777','--tmpfs','/tmp:rw,mode=1777','--entrypoint','redis-server',image,'--save','','--appendonly','no','--bind','127.0.0.1')
 data['owned_containers'].append(redis_id);save()
 ready_by=time.monotonic()+10
 while True:
  try:
   info=command('exec',redis_id,'redis-cli','INFO','server')
   data['redis_run_id']=next(line.split(':',1)[1] for line in info.splitlines() if line.startswith('run_id:'))
   break
  except subprocess.CalledProcessError:
   assert time.monotonic()<ready_by, 'owned Redis did not become ready'
   time.sleep(.1)
 def create(role,script,options):
  args=['create','--pull=never','--name',name+'-'+role,'--label','oscar.corrtest-persistence='+name,'--user','1000:1000','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/tmp:rw,mode=1777','--network','container:'+redis_id,'-v',str(R/'oscar-correlator/src')+':/backend:ro','-v',str(folder)+':/qualification','-e','PYTHONDONTWRITEBYTECODE=1','-e','USE_VAULT=false','-e','CORRTEST_PERSISTENCE_REDIS_URL=redis://127.0.0.1:6379/0','--entrypoint','python',controller,'-B','/qualification/'+script,'--source','/backend','--source-sha256',data['source_sha256'],'--isolated-redis-run-id',data['redis_run_id'],*options]
  data['commands'].append(['docker',*args]); identity=command(*args);data['owned_containers'].append(identity);save();return identity
 prepare=create('prepare','run.py',['--prepare-kill','--output','/qualification/kill-3-prepare'])
 command('start',prepare)
 checkpoint=folder/'kill-3-prepare/checkpoint.json';until=time.monotonic()+15
 while not checkpoint.exists():
  assert time.monotonic()<until,'checkpoint readiness timeout'
  time.sleep(.05)
 body=json.loads(checkpoint.read_text());data['checkpoint_sha256']=digest(checkpoint)
 assert time.time()*1000 < body['first_seen_ms']+10000,'missed kill window'
 command('kill','--signal=KILL',prepare)
 state=json.loads(command('inspect',prepare))[0]['State'];data['prepare_state']=state
 assert not state['Running'] and state['Pid']==0 and state['ExitCode']==137 and state['OOMKilled'] is False
 assert not (checkpoint.parent/'result.json').exists()
 data['killed_at_ms']=int(time.time()*1000)
 data['preopen_sqlite']={p.name:dict(sha256=digest(p),size=p.stat().st_size) for p in checkpoint.parent.glob('timers.sqlite*')}
 (folder/'kill-3-prepare.log').write_text(command('logs',prepare))
 save()
 resume=create('resume','resume.py',['--prepare','/qualification/kill-3-prepare','--checkpoint-sha256',data['checkpoint_sha256'],'--output','/qualification/kill-3-resume'])
 with (folder/'kill-3-resume.log').open('x') as output:
  result=subprocess.run(['docker','start','-a',resume],stdout=output,stderr=subprocess.STDOUT,timeout=80)
 state=json.loads(command('inspect',resume))[0]['State'];data['resume_state']=state;data['returncode']=state['ExitCode'];data['docker_start_returncode']=result.returncode
finally:
 for identity in reversed(data['owned_containers']):
  info=json.loads(command('inspect',identity))[0];assert info['Config']['Labels']['oscar.corrtest-persistence']==name
  assert not any(m['Type']=='volume' for m in info['Mounts'])
  command('rm','-f',identity)
  absent=subprocess.run(['docker','inspect',identity],capture_output=True,text=True,timeout=30)
  assert absent.returncode!=0 and 'no such object' in absent.stderr.lower() and identity in absent.stderr
  data['removed'].append(identity)
 data['fixture_after']={n:digest(folder/n) for n in before};data['fixture_parity']=data['fixture_after']==before;save()
print(json.dumps({k:data.get(k) for k in ['returncode','fixture_parity','owned_containers','removed']}))
assert data['returncode']==0 and data['fixture_parity'] and len(data['removed'])==3
