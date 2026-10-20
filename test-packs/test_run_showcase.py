"""Finite showcase harness boundary tests; mock HTTP is not deployed UAT."""
import importlib.util
import json
from datetime import datetime, timezone, timedelta
from pathlib import Path
import httpx
import pytest
import build_showcase as build
import install_operational as installed

HERE = Path(__file__).resolve().parent
BACKEND = HERE.parents[1] / 'oscar'
ORIGIN = 'https://owned-target.test'
TOKEN = 'only-for-test-never-retained'
GATE = None

@pytest.fixture(scope='module', autouse=True)
def real_runtime_gate():
    global GATE
    code='''
import json
from app.core.fail_open_poller import FailOpenState
state=FailOpenState(authoritative=True)
state.update('true')
breaker=state.observation(20)
print(json.dumps({'authoritative':state.authoritative,'dispatch_enabled':state.correlator_dispatch_enabled,'breaker':breaker,'operational':state.correlator_dispatch_enabled and breaker['state']=='healthy'}))
'''
    GATE=build.common.backend_call(BACKEND,['oscar-correlator/src'],code,b'',label='actual dispatch gate')

@pytest.fixture(scope='module')
def harness():
    path = HERE / 'run_showcase.py'
    assert path.is_file(), 'finite executable showcase harness missing'
    spec = importlib.util.spec_from_file_location('showcase_harness', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

@pytest.fixture(scope='module')
def distribution(tmp_path_factory):
    root = tmp_path_factory.mktemp('showcase') / 'kit'
    build.build(BACKEND, root)
    return root

class Clock:
    def __init__(self): self.t = datetime(2026, 10, 20, 12, tzinfo=timezone.utc); self.m = 0
    def now(self): return self.t
    def monotonic(self): return self.m
    def sleep(self, seconds): self.t += timedelta(seconds=seconds); self.m += seconds

class Peer:
    def __init__(self, harness, distribution, clock, evidence, case):
        self.h, self.clock, self.evidence, self.case = harness, clock, evidence, case
        self.distribution = distribution
        self.manifest = build.verify(distribution)
        self.bodies, self.calls = [], []
        self.failure = None
        self.rules = [{**rule, 'enabled': True, 'id': i+2} for i, rule in enumerate(build.rules())]
        self.rules.insert(0, {**harness.retained_v1(), 'id':1})
    def history(self, rowid, labels, fingerprint):
        return {'ID': f'00000000-0000-4000-8000-{rowid:012d}', 'alertname': labels['alertname'],
                'fingerprint': fingerprint, 'status': 'firing', 'severity': labels['severity'],
                'startsAt': self.bodies[min(rowid-1,len(self.bodies)-1)]['startsAt'], 'createdAt': self.bodies[min(rowid-1,len(self.bodies)-1)]['startsAt'],
                'labels': [{'Label': k, 'Value': v} for k,v in labels.items()], 'annotations': []}
    def handle(self, request):
        self.calls.append((request.method, request.url.path))
        assert request.headers['X-API-Key'] == TOKEN
        path = request.url.path
        if request.method == 'POST':
            assert path == '/webhookreceiver/webhook/corrtest_showcase_v2'
            assert any(json.loads(p.read_text())['request']['method']=='POST' for p in self.evidence.glob('*-before.json'))
            assert request.headers['If-Match'] == '"'+self.manifest['profile']['effective_sha256']+'"'
            self.bodies.append(json.loads(request.content))
            if self.failure=='lost': raise httpx.ReadTimeout('uncertain', request=request)
            if self.failure=='interrupt': raise KeyboardInterrupt('cancelled send')
            if self.failure=='clock-back': self.clock.t -= timedelta(seconds=1)
            if self.failure=='html502': return httpx.Response(502,text='<html>upstream failed</html>')
            if self.failure=='bad200': return httpx.Response(200,text='malformed',headers={'X-Oscar-Profile-Revision':self.manifest['profile']['effective_sha256']})
            return httpx.Response(200, json={'status':'ok'}, headers={'X-Oscar-Profile-Revision':self.manifest['profile']['effective_sha256']})
        if path.endswith('/correlation_rules/runtime'):
            value={'checked_at_ms': 1792497600000, 'load_status':'loaded', 'loaded_at':'2026-10-20T11:59:00Z', 'last_attempt_ms':1792497540000, 'loaded_rule_ids':list(range(1,9)), 'loaded_total':8, 'loaded_omitted':0, 'rejected_total':0, 'rejected_omitted':0, 'rejected_rules':[], **GATE, 'dispatch_enabled':self.failure!='dispatch', 'enhanced_correlation':{'production_enabled':False}, 'dependencies':{'status':'ready',**dict.fromkeys(['nats','consumer','redis','lua_registered','timer_store'],True)}}
            if self.failure=='stale-breaker': value['breaker']={**value['breaker'],'fresh':False}
            if self.failure=='enhanced': value['enhanced_correlation']['production_enabled']=True
        elif path.endswith('/correlation_rules'):
            value={'rows':self.rules,'total':8,'page':1,'perPage':100}
            if self.failure=='changed-v1': self.rules[0]['window_seconds']=61
        elif path.endswith('/packs'): value={'packs':[{'pack_id':'corrtest_showcase','pack_version':'0.2.0','manifest_status':'valid','pack_content_hash':self.h.pack_identity(self.distribution)[0]}]}
        elif '/configuration/items/' in path:
            state=self.h.pack_identity(self.distribution)[1]
            value={'state':'up_to_date','changed_fields':[],'pack':state,'live':state,'pack_hash':installed.canonical_hash(state),'live_hash':installed.canonical_hash(state),'applied_hash':installed.canonical_hash(state)}
        elif path.endswith('/alerts/active-alerts'):
            rows=[{'fingerprint':f'{i+1:016x}', 'starts_at':body['startsAt'],'alertname':body['labels']['alertname'], 'alert_labels':[{'name':k,'value':v} for k,v in body['labels'].items()]} for i,body in enumerate(self.bodies)]
            value={'records':rows,'total_records':len(rows),'total_pages':int(bool(rows))}
        elif path.endswith('/history'):
            name=json.loads(request.url.params['filter'])['items'][0]['value']
            rows=[]
            for i,body in enumerate(self.bodies):
                if body['labels']['alertname']==name:
                    rows.append(self.history(i+1,{**body['labels'],'am_fingerprint':f'{i+1:016x}','oscar_fingerprint':f'oscar-{i+1}'},f'oscar-{i+1}'))
            if name.endswith('_synthetic_parent') and self.bodies and self.case['expected_parent_history'] and self.failure!='missing-parent':
                rule=next(r for r in self.rules if r.get('name')=='corrtest-showcase-v2-'+self.case['pattern'])
                window=f'{rule["id"]}:012345678abc:1792497600000'
                labels={'alertname':name,'qualification_pack':build.PROFILE,'qualification_run':self.bodies[0]['labels']['qualification_run'],'site':self.case['site'],'severity':'critical','oscar_synthetic_source':'correlator','oscar_correlation_rule_id':str(rule['id']),'oscar_correlation_rule_name':rule['name'],'oscar_correlation_window_id':window,'correlation_parent_identity':self.h.parent_identity(rule['id'],window),'am_fingerprint':'parent-history','oscar_fingerprint':'parent-history'}
                rows=[self.history(90,labels,'parent-history')]
                if self.case['pattern']=='persistence' and self.failure!='early-parent':
                    rows[0]['startsAt']=rows[0]['createdAt']=self.h.stamp(self.h.instant(self.bodies[0]['startsAt'])+timedelta(seconds=30))
            value={'records':rows,'total_records':len(rows),'total_pages':int(bool(rows)),'page':1,'per_page':100}
        elif path.endswith('/correlation_rules/audit'):
            i=int(request.url.params['fingerprint'],16)-1
            rule=next(r for r in self.rules if r.get('name')=='corrtest-showcase-v2-'+self.case['pattern'])
            triggered=i==len(self.bodies)-1 and self.case['expected_parent_history']
            rows=[{'id':i+1,'created_at':self.bodies[i]['startsAt'],'alert_fingerprint':f'{i+1:016x}','rule_id':rule['id'],'rule_name':rule['name'],'pattern':rule['pattern'],'outcome':'parent_emitted' if triggered else 'enriched','window_id':f'{rule["id"]}:012345678abc:1792497600000','parent_fingerprint':'parent-history' if triggered else None,'reason':None}]
            if self.case['pattern']=='persistence' and self.case['polarity']=='negative': rows=[]
            if self.case['pattern']=='persistence' and self.case['polarity']=='positive' and self.failure!='early-parent':
                rows[0]['created_at']=self.h.stamp(self.h.instant(self.bodies[0]['startsAt'])+timedelta(seconds=30))
            if self.case['pattern']=='parent_child' and self.case['polarity']=='positive':
                rows[0]['parent_fingerprint']='0000000000000001'
                rows[0]['reason']='parent_registered' if i==0 else 'parent_child_active_parent_present tag_only parent_fp=0000000000000001'
            value={'rows':rows,'total':len(rows),'page':1,'perPage':100}
        else: raise AssertionError(path)
        if self.failure=='truncated-history' and path.endswith('/history'): value['total_records']+=1
        if self.failure=='truncated-audit' and path.endswith('/audit'): value['total']+=1
        return httpx.Response(200,json=value)

def execute(harness, distribution, tmp_path, case_id, failure=None):
    case=next(c for c in build.scenario()['cases'] if c['id']==case_id)
    clock=Clock(); evidence=tmp_path/'run'; peer=Peer(harness,distribution,clock,evidence,case); peer.failure=failure
    owner={'format':'corrtest-owned-target-v1','target':ORIGIN,'owner_reference':'fixture','exclusive_through_export':True,'stable_configuration_and_processes':True,'destinations_history_or_controlled_lab':True}
    report=harness.run(distribution,build.common.digest((distribution/'manifest.json').read_bytes()),ORIGIN,ORIGIN+'/webhookreceiver',TOKEN,TOKEN,owner,evidence,case_id,transport=httpx.MockTransport(peer.handle),now=clock.now,monotonic=clock.monotonic,sleep=clock.sleep)
    return report,peer,evidence

@pytest.mark.parametrize('pattern',build.PATTERNS)
@pytest.mark.parametrize('polarity',['positive','negative'])
def test_finite_full_path_fixture_and_independent_verifier(harness,distribution,tmp_path,pattern,polarity):
    report,peer,evidence=execute(harness,distribution,tmp_path,pattern+'-'+polarity)
    assert report['verdict']=='PASS',report
    assert report['cleanup']=='DIRTY'
    assert len(peer.bodies)==len(peer.case['inputs'])
    assert all(method=='GET' or path=='/webhookreceiver/webhook/corrtest_showcase_v2' for method,path in peer.calls)
    assert harness.verify(evidence,report['manifest_sha256'])['verdict']=='PASS'
    assert TOKEN not in ''.join(p.read_text() for p in evidence.glob('*.json'))

@pytest.mark.parametrize('failure',['lost','missing-parent','dispatch'])
def test_no_retry_and_no_false_parent_pass(harness,distribution,tmp_path,failure):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive',failure)
    assert report['verdict']!='PASS'
    assert report['cleanup']==('UNKNOWN' if failure=='lost' else 'DIRTY' if peer.bodies else 'NOT_REQUIRED')
    if failure=='lost': assert len(peer.bodies)==1
    if failure=='dispatch': assert not peer.bodies
    assert harness.verify(evidence,report['manifest_sha256'])['verdict']!='PASS'

def test_export_verifier_rejects_changed_parent_and_report(harness,distribution,tmp_path):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive')
    document=json.loads((evidence/'report.json').read_bytes()); document['case']='flood-negative'
    (evidence/'report.json').write_text(json.dumps(document))
    with pytest.raises(ValueError): harness.verify(evidence,report['manifest_sha256'])

def rebind(evidence):
    path=evidence/'evidence-manifest.json'
    doc=json.loads(path.read_bytes())
    doc['files']={name:build.common.digest((evidence/name).read_bytes()) for name in doc['files']}
    path.write_bytes(build.common.canonical(doc))

@pytest.mark.parametrize('damage',['observation','runtime','send'])
def test_verifier_requires_original_http_not_rebound_summary(harness,distribution,tmp_path,damage):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive')
    if damage=='observation':
        path=evidence/'observation.json'; doc=json.loads(path.read_bytes())
        # A summary can claim the same parent using a different raw History ID.
        parent=next(value for key,value in doc['history'].items() if key.endswith('_synthetic_parent'))
        parent['records'][0]['ID']='00000000-0000-4000-8000-000000000091'
    elif damage=='runtime':
        path=evidence/'preflight.json'; doc=json.loads(path.read_bytes()); doc['runtime']['loaded_at']='2026-10-20T11:58:00Z'
        other=evidence/'postflight.json'; other.write_bytes(build.common.canonical(doc))
    else:
        path=next(path for path in evidence.glob('*-before.json') if json.loads(path.read_bytes())['request']['method']=='POST')
        doc=json.loads(path.read_bytes()); doc['request']['base']='https://elsewhere.test/webhookreceiver'
    path.write_bytes(build.common.canonical(doc)); rebind(evidence)
    with pytest.raises(ValueError): harness.verify(evidence,report['manifest_sha256'])

def test_cancelled_send_retains_unknown_and_stops(harness,distribution,tmp_path):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive','interrupt')
    assert report['verdict']=='INCONCLUSIVE'
    assert report['cleanup']=='UNKNOWN'
    assert len(peer.bodies)==1
    assert harness.verify(evidence,report['manifest_sha256'])['cleanup']=='UNKNOWN'

@pytest.mark.parametrize('failure',['html502','bad200'])
def test_unparseable_http_outcome_is_retained_unknown(harness,distribution,tmp_path,failure):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive',failure)
    assert report['verdict']=='INCONCLUSIVE' and report['cleanup']=='UNKNOWN'
    assert len(peer.bodies)==1
    assert harness.verify(evidence,report['manifest_sha256'])['cleanup']=='UNKNOWN'

def test_persistence_rejects_parent_before_thirty_seconds(harness,distribution,tmp_path):
    report,peer,evidence=execute(harness,distribution,tmp_path,'persistence-positive','early-parent')
    assert report['verdict']=='INCONCLUSIVE' and report['cleanup']=='DIRTY'

def test_live_clock_advances_between_recording_and_report(harness,distribution,tmp_path,monkeypatch):
    old=Clock.now
    def ticking(self):
        self.sleep(.001)
        return old(self)
    monkeypatch.setattr(Clock,'now',ticking)
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive')
    assert report['verdict']=='PASS',report
    assert harness.verify(evidence,report['manifest_sha256'])['verdict']=='PASS'

@pytest.mark.parametrize('field',['ownership','last_forward_at'])
def test_rebound_claims_cannot_bypass_ownership_or_original_timing(harness,distribution,tmp_path,field):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive')
    document=json.loads((evidence/'report.json').read_bytes())
    if field=='ownership': document[field]['exclusive_through_export']=False
    else: document[field]=harness.stamp(harness.instant(document[field])+timedelta(seconds=1))
    (evidence/'report.json').write_bytes(build.common.canonical(document)); rebind(evidence)
    with pytest.raises(ValueError): harness.verify(evidence,report['manifest_sha256'])

@pytest.mark.parametrize('failure',['changed-v1','truncated-history','truncated-audit'])
def test_inventory_drift_and_incomplete_pages_never_pass(harness,distribution,tmp_path,failure):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive',failure)
    assert report['verdict']=='INCONCLUSIVE'
    if failure=='changed-v1': assert not peer.bodies
    assert harness.verify(evidence,report['manifest_sha256'])['verdict']=='INCONCLUSIVE'

def test_backward_wall_clock_stops_and_retains_uncertain_send(harness,distribution,tmp_path):
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive','clock-back')
    assert report['verdict']=='INCONCLUSIVE' and report['cleanup']=='UNKNOWN'
    assert len(peer.bodies)==1
    assert 'clock regressed' in report['error']
    assert harness.verify(evidence,report['manifest_sha256'])['verdict']=='INCONCLUSIVE'

@pytest.mark.parametrize('failure',['stale-breaker','enhanced'])
def test_actual_dispatch_gate_requires_fresh_breaker_and_disabled_enhanced(harness,distribution,tmp_path,failure):
    assert GATE['authoritative'] is True and GATE['dispatch_enabled'] is True
    report,peer,evidence=execute(harness,distribution,tmp_path,'flood-positive',failure)
    assert report['verdict']=='INCONCLUSIVE' and report['cleanup']=='NOT_REQUIRED'
    assert not peer.bodies
