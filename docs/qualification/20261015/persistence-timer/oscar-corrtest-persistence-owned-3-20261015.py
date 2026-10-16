import hashlib,json,pathlib,subprocess,uuid,time
C=pathlib.Path('/Users/coreyellis/code/github.com/cmetech/oscar_app/.worktrees/correlation-reliability-20260919/oscar-corrtest')
R=C.parent/'oscar'; folder=C/'docs/qualification/20261015/persistence-timer'; manifest=folder/'actual-3-owner.json'
assert not manifest.exists()
name='oscar-corrtest-persistence-'+uuid.uuid4().hex[:12]
def command(*args):return subprocess.run(['docker',*args],capture_output=True,text=True,check=True,timeout=30).stdout.strip()
image=command('image','inspect','redis:7-alpine','--format','{{.Id}}')
controller='sha256:e3a34f7d4a024c6f991a8a6477770ab0a5ae2ad809f80d544e78e908ff63d4c3'
assert command('image','inspect',controller,'--format','{{.Id}}')==controller
runner=folder/'run.py';before=hashlib.sha256(runner.read_bytes()).hexdigest()
data=dict(name=name,image=image,controller_image=controller,runner_before=before,source_sha256='1d3b9e40fc4de1415245fe34fcaa682bf0fb66aee49890516f613205c858757c',owned_containers=[],removed=[])
manifest.write_text(json.dumps(data,indent=2)+'\n')
try:
 container=command('run','-d','--pull=never','--name',name,'--label','oscar.corrtest-persistence='+name,'--user','1000:1000','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/data:rw,mode=1777','--tmpfs','/tmp:rw,mode=1777','--entrypoint','redis-server',image,'--save','','--appendonly','no','--bind','127.0.0.1')
 data['owned_containers'].append(container)
 ready_by=time.monotonic()+10
 while True:
  try:
   server=command('exec',container,'redis-cli','INFO','server');break
  except subprocess.CalledProcessError:
   assert time.monotonic()<ready_by, 'owned Redis startup timeout'
   time.sleep(.1)
 data['redis_run_id']=next(line.split(':',1)[1] for line in server.splitlines() if line.startswith('run_id:'))
 args=['create','--pull=never','--name',name+'-runner','--label','oscar.corrtest-persistence='+name,'--user','1000:1000','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs','/tmp:rw,mode=1777','--network','container:'+container,'-v',str(R/'oscar-correlator/src')+':/backend:ro','-v',str(folder)+':/qualification','-e','PYTHONDONTWRITEBYTECODE=1','-e','USE_VAULT=false','-e','CORRTEST_PERSISTENCE_REDIS_URL=redis://127.0.0.1:6379/0','--entrypoint','python',controller,'-B','/qualification/run.py','--source','/backend','--source-sha256',data['source_sha256'],'--isolated-redis-run-id',data['redis_run_id'],'--output','/qualification/actual-3']
 data['command']=['docker',*args];worker=command(*args);data['owned_containers'].append(worker)
 manifest.write_text(json.dumps(data,indent=2)+'\n')
 with (folder/'actual-3-run.log').open('x') as output:
  result=subprocess.run(['docker','start','-a',worker],stdout=output,stderr=subprocess.STDOUT,timeout=80)
 state=json.loads(command('inspect',worker))[0]['State']
 data['returncode']=state['ExitCode']; data['docker_start_returncode']=result.returncode
finally:
 for identity in reversed(data['owned_containers']):
  info=json.loads(command('inspect',identity))[0]
  assert info['Config']['Labels']['oscar.corrtest-persistence']==name
  assert not any(m['Type']=='volume' for m in info['Mounts'])
  command('rm','-f',identity)
  absent=subprocess.run(['docker','inspect',identity],capture_output=True,text=True,timeout=30)
  assert absent.returncode!=0 and 'no such object' in absent.stderr.lower() and identity in absent.stderr
  data['removed'].append(identity)
 data['runner_after']=hashlib.sha256(runner.read_bytes()).hexdigest(); data['runner_parity']=data['runner_after']==before
 manifest.write_text(json.dumps(data,indent=2)+'\n')
print(json.dumps({k:data[k] for k in ('returncode','runner_parity','owned_containers','removed')}))
assert data['returncode']==0 and data['runner_parity'] and len(data['removed'])==2
