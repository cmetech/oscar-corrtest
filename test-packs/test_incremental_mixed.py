"""Offline adversarial tests; no target access or service fixtures."""
import base64
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import pytest

HERE = Path(__file__).resolve().parent
START = datetime(2026, 10, 23, 12, 0, 1, tzinfo=timezone.utc)
REV = 'f' * 64


def module():
    path = HERE / 'incremental_mixed.py'
    assert path.exists(), 'incremental mixed planner/oracle has not been implemented'
    spec = importlib.util.spec_from_file_location('incremental_mixed', path)
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value)
    return value


@pytest.mark.parametrize('rate,packets,total', [(10,101,10440),(15,151,15660),(20,202,20880),(25,252,26100)])
@pytest.mark.parametrize('phase', [0,39,58])
def test_full_profile_counts_and_real_minute_window_packing(rate,packets,total,phase):
    m = module(); p = m.make_plan('offline',rate,START.replace(second=phase))
    assert p['total'] == total and p['correlation_inputs'] == packets*31
    assert p['ordinary_inputs'] == total-packets*31 and p['packets'] == packets
    assert p['phases'] == {'ramp_up':[0,120], 'hold':[120,1020], 'ramp_down':[1020,1140]}
    assert len({e['source_id'] for e in p['events']}) == total
    by = {e['source_id']:e for e in p['events']}
    for g in p['groups']:
        times = [by[s]['at_seconds'] for s in g['sources']]
        assert all(b-a >= 3 for a,b in zip(times,times[1:]))
        absolute = [int((m.instant(p['start_at'])+timedelta(seconds=t)).timestamp())//60 for t in times]
        assert len(set(absolute)) == 1
    assert len(p['groups']) == packets*14
    assert p['synthetic_parents'] == packets*6 and p['existing_parent_links'] == packets


def fixture():
    m = module(); event = {'seq':0,'at_seconds':0,'source_id':'test-one','alertname':'MixedOrdinary_test_0',
        'site':'ordinary-0','device':'ordinary','oscar_source':'api','case':'ordinary','group':None}
    p = {'run':'test','start_at':m.stamp(START),'events':[event],'groups':[],'total':1,
         'synthetic_parents':0,'existing_parent_links':0,'rules':{},'limits':m.DEFAULT_LIMITS.copy()}
    payload=m.body(event,p,START)
    row=m.record_exchange(p,event,START,200,b'{"status":"ok"}',[REV],START+timedelta(seconds=.1))
    h={'id':'h-one','fingerprint':'oscar-one','alertname':event['alertname'],'status':'firing',
       'original_severity':'warning','startsAt':payload['startsAt'],'created_at':m.stamp(START+timedelta(seconds=1)),
       'received':1,'labels':[{'Label':k,'Value':v} for k,v in {**payload['labels'],'am_fingerprint':'native-one','oscar_fingerprint':'oscar-one'}.items()]}
    a={'id':1,'alert_fingerprint':'native-one','parent_fingerprint':None,'rule_id':None,'rule_name':None,
       'pattern':None,'window_id':None,'outcome':'released_no_trigger','reason':'no_matching_rules','created_at':h['created_at']}
    return m,p,[row],[h],[a]


def test_valid_ordinary_physical_source_passes_and_plan_roundtrips(tmp_path):
    m,p,l,h,a=fixture(); path=tmp_path/'plan.json'; pin=m.write_plan(path,p)
    assert m.read_plan(path,pin)==p
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='PASS'
    path.write_text(path.read_text()+' ')
    with pytest.raises(ValueError):m.read_plan(path,pin)


@pytest.mark.parametrize('mutation', ['missing-source','wrong-state','duplicate-id','duplicate-fingerprint','bad-ack','bad-revision',
    'payload-plan-conflict','body-byte-conflict','response-byte-conflict','late-vector','early-history','missing-audit','foreign-rule','duplicate-audit-id'])
def test_false_pass_mutations_reject(mutation):
    m,p,l,h,a=fixture()
    if mutation=='missing-source':h=[]
    elif mutation=='wrong-state':h[0]['status']='resolved'
    elif mutation in ('duplicate-id','duplicate-fingerprint'):h.append(deepcopy(h[0]));h[-1]['id']='other' if mutation=='duplicate-fingerprint' else h[-1]['id']
    elif mutation=='bad-ack':l[0]['status']=500;l[0]['accepted']=True
    elif mutation=='bad-revision':l[0]['profile_revisions']=[]
    elif mutation=='payload-plan-conflict':p['events'][0]['alertname']='foreign'
    elif mutation=='body-byte-conflict':l[0]['request_body_base64']=base64.b64encode(b'{}').decode()
    elif mutation=='response-byte-conflict':l[0]['response_body_sha256']='0'*64
    elif mutation=='late-vector':l[0]['wall']=m.stamp(START+timedelta(seconds=60))
    elif mutation=='early-history':h[0]['created_at']=m.stamp(START-timedelta(seconds=1))
    elif mutation=='missing-audit':a=[]
    elif mutation=='foreign-rule':a[0]['rule_id']=99
    elif mutation=='duplicate-audit-id':a.append(deepcopy(a[0]))
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']!='PASS'


def parent_fixture():
    m,p,l,h,a=fixture(); p['groups']=[{'site':'g','case':'parent_child-positive','pattern':'parent_child',
      'positive':True,'sources':['parent','child'],'expected_parents':0,'expected_links':1,'parent_alertname':'unused'}]
    p['rules']={'parent_child':{'id':8,'name':'corrtest-showcase-v2-parent_child'}};p['total']=2;p['existing_parent_links']=1
    p['events']=[];l=[];h=[];a=[]
    for i,role in enumerate(['parent','child']):
        now=START+timedelta(seconds=3*i);ev={'seq':i,'at_seconds':3*i,'source_id':role,'alertname':'CorrTestShowcaseV2_parent_child_'+role,'site':'g','device':'d'+role,'oscar_source':'snmp','case':'parent_child-positive','group':'g'}
        p['events'].append(ev);l.append(m.record_exchange(p,ev,now,200,b'{"status":"ok"}',[REV],now+timedelta(seconds=.1)))
        labels={**l[-1]['body']['labels'],'am_fingerprint':role,'oscar_fingerprint':'oscar-'+role,'oscar_correlation_rule_id':'8',
          'oscar_correlation_rule_name':p['rules']['parent_child']['name'],'oscar_correlation_pattern':'parent_child','oscar_correlation_role':role,
          'oscar_correlation_parent_fingerprint':'parent','oscar_correlation_window_id':'window-'+role}
        h.append({'id':'h'+role,'fingerprint':'oscar-'+role,'alertname':ev['alertname'],'status':'firing','original_severity':'warning','startsAt':m.stamp(now),'created_at':m.stamp(now+timedelta(seconds=1)),'received':1,'labels':[{'Label':k,'Value':v} for k,v in labels.items()]})
        a.append({'id':i+1,'alert_fingerprint':role,'parent_fingerprint':'parent','rule_id':8,'rule_name':p['rules']['parent_child']['name'],'pattern':'parent_child','window_id':'window-'+role,'outcome':'enriched','reason':'parent_registered' if i==0 else 'parent_child_active_parent_present tag_only','created_at':m.stamp(now+timedelta(seconds=1))})
    return m,p,l,h,a


def test_distinct_effect_not_repeated_audit_is_parent_count():
    m,p,l,h,a=parent_fixture();a.append({**a[-1],'id':3})
    report=m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))
    assert report['verdict']=='PASS',report
    assert report['existing_parent_links']==1 and report['repeat_audit_rows']==1


@pytest.mark.parametrize('key,value', [('oscar_correlation_parent_fingerprint',None),('oscar_correlation_role','parent'),('oscar_correlation_window_id','foreign'),('oscar_correlation_rule_id','99')])
def test_physical_parent_child_binding_rejects_corruption(key,value):
    m,p,l,h,a=parent_fixture();assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='PASS'
    h[1]['labels']=[x for x in h[1]['labels'] if x['Label']!=key]
    if value is not None:h[1]['labels'].append({'Label':key,'Value':value})
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


def test_actual_minute_straddle_and_reversed_parent_child_are_invalid_vectors():
    m,p,l,h,a=parent_fixture();late=START.replace(second=59)
    for i in range(2):l[i]=m.record_exchange(p,p['events'][i],late+timedelta(seconds=i*3),200,b'{"status":"ok"}',[REV],late+timedelta(seconds=i*3+.1))
    assert m.validate_inputs(p,l,REV)
    l.reverse();l[0]['wall'],l[1]['wall']=l[1]['wall'],l[0]['wall']
    assert m.validate_inputs(p,l,REV)


def test_phase_summary_does_not_mix_hold_and_drain():
    m,p,l,h,a=fixture();p['phases']={'hold':[0,10],'drain':[10,20]}
    samples=[{'at_seconds':2,'queues':{'history':1}},{'at_seconds':12,'queues':{'history':999}}]
    report=m.summarize_samples(p,l,samples)
    assert report['hold']['sampled_queue_peaks']['history']==1
    assert report['drain']['sampled_queue_peaks']['history']==999


def test_planned_windows_leave_room_for_allowed_sender_lateness():
    m=module();p=m.make_plan('margin',25,START.replace(second=58));by={e['source_id']:e for e in p['events']}
    for g in p['groups']:
        times=[(m.instant(p['start_at'])+timedelta(seconds=by[s]['at_seconds'])).timestamp() for s in g['sources']]
        assert int(times[0])//60==int(times[-1]+p['limits']['input_lateness_seconds'])//60


def synthetic_fixture():
    m,p,l,h,a=fixture();p['events'][0].update(alertname='CorrTestShowcaseV2_persistence_signal',case='persistence-positive',group='ordinary-0')
    l=[m.record_exchange(p,p['events'][0],START,200,b'{"status":"ok"}',[REV],START+timedelta(seconds=.1))]
    h[0]['alertname']=p['events'][0]['alertname'];h[0]['labels']=[{'Label':k,'Value':v} for k,v in {**l[0]['body']['labels'],'am_fingerprint':'native-one','oscar_fingerprint':'oscar-one'}.items()]
    rule={'id':7,'name':'corrtest-showcase-v2-persistence'};p['rules']={'persistence':rule};p['synthetic_parents']=1
    p['groups']=[{'site':'ordinary-0','case':'persistence-positive','pattern':'persistence','positive':True,'sources':['test-one'],'expected_parents':1,'expected_links':0,'parent_alertname':'PParent'}]
    a[0].update(rule_id=7,rule_name=rule['name'],pattern='persistence',window_id='w',outcome='parent_emitted',parent_fingerprint='parent',reason='persistence_triggered',created_at=m.stamp(START+timedelta(seconds=31)))
    labels={'qualification_run':'test','qualification_pack':'corrtest_showcase_v2','site':'ordinary-0','oscar_synthetic_source':'correlator','oscar_correlation_rule_id':'7','oscar_correlation_rule_name':rule['name'],'oscar_correlation_window_id':'w','correlation_parent_identity':m.showcase.parent_identity(7,'w'),'am_fingerprint':'parent','oscar_fingerprint':'parent'}
    h.append({'id':'h-parent','fingerprint':'parent','alertname':'PParent','status':'firing','received':1,'occurrences':1,'original_severity':'critical','startsAt':m.stamp(START+timedelta(seconds=31)),'created_at':m.stamp(START+timedelta(seconds=31)),'labels':[{'Label':k,'Value':v} for k,v in labels.items()]})
    return m,p,l,h,a


def test_synthetic_parent_repeated_trigger_audits_are_one_physical_effect():
    m,p,l,h,a=synthetic_fixture();a.append({**a[0],'id':2})
    report=m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))
    assert report['verdict']=='PASS',report
    assert report['physical_synthetic_parents']==1 and report['repeat_audit_rows']==1


@pytest.mark.parametrize('mutation',['missing-parent','wrong-parent-state','wrong-parent-pack','duplicate-parent','premature-parent','late-parent','conflicting-trigger','snapshot-late','ack-duplicate-key'])
def test_synthetic_and_snapshot_false_pass_mutations_reject(mutation):
    m,p,l,h,a=synthetic_fixture()
    if mutation=='missing-parent':h.pop()
    elif mutation=='wrong-parent-state':h[-1]['status']='resolved'
    elif mutation=='wrong-parent-pack':next(x for x in h[-1]['labels'] if x['Label']=='qualification_pack')['Value']='foreign'
    elif mutation=='duplicate-parent':h.append(deepcopy(h[-1]));h[-1]['id']='another-parent';h[-1]['fingerprint']='another-parent'
    elif mutation=='premature-parent':h[-1]['startsAt']=m.stamp(START+timedelta(seconds=20))
    elif mutation=='late-parent':h[-1]['created_at']=m.stamp(START+timedelta(seconds=150))
    elif mutation=='conflicting-trigger':a.append({**a[0],'id':2,'parent_fingerprint':'foreign'})
    elif mutation=='ack-duplicate-key':
        raw=b'{"status":"bad","status":"ok"}';l[0]['response_body_base64']=base64.b64encode(raw).decode();l[0]['response_body_sha256']=hashlib.sha256(raw).hexdigest()
    end=m.stamp(START+timedelta(seconds=200)) if mutation=='snapshot-late' else m.stamp(START+timedelta(seconds=94))
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=end)['verdict']!='PASS'


def test_resolved_source_state_is_explicit_not_silently_accepted():
    m,p,l,h,a=fixture();p['events'][0]['expected_state']='resolved';h[0]['status']='resolved'
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='PASS'


@pytest.mark.parametrize('rate',[10,15,20,25])
def test_pilot_is_explicitly_shorter_with_same_planner_contract(rate):
    m=module();p=m.make_plan('pilot',rate,START,profile='pilot')
    assert p['duration_seconds']==80 and p['phases']['hold']==[10,70]
    assert p['total']==rate*72


@pytest.mark.parametrize('mutation',['suppressed-decision','missing-null-fields','unbound-revision','wrong-parent-registration'])
def test_decision_and_revision_contract_cannot_silently_weaken(mutation):
    if mutation=='wrong-parent-registration':
        m,p,l,h,a=parent_fixture();a[0]['reason']='foreign'
    else:
        m,p,l,h,a=fixture()
        if mutation=='suppressed-decision':a[0]['outcome']='suppressed_global'
        elif mutation=='missing-null-fields':a[0].pop('rule_id')
        elif mutation=='unbound-revision':
            l[0]['profile_revisions']=[''];assert m.evaluate(p,l,h,a,'',snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']!='PASS';return
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']!='PASS'


def test_sql_utc_timestamps_without_zone_are_read_as_utc():
    m,p,l,h,a=fixture();h[0]['created_at']=h[0]['created_at'].replace('Z','');h[0]['startsAt']=h[0]['startsAt'].replace('Z','')
    a[0]['created_at']=a[0]['created_at'].replace('Z','')
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='PASS'


def test_explicit_receiver_rejection_is_failed_stage_not_invalid_vector():
    m,p,l,h,a=fixture();l[0]['status']=503
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


def test_invalid_actual_scheduling_is_inconclusive_without_backdating():
    m,p,l,h,a=fixture();l[0]=m.record_exchange(p,p['events'][0],START+timedelta(seconds=10),200,b'{"status":"ok"}',[REV],START+timedelta(seconds=10.1))
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='INCONCLUSIVE'


def test_missing_completed_snapshot_cannot_qualify_stage():
    m,p,l,h,a=fixture()
    assert m.evaluate(p,l,h,a,REV)['verdict']=='INCONCLUSIVE'


def test_too_early_snapshot_cannot_qualify_negative_observation():
    m,p,l,h,a=fixture()
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=2)))['verdict']=='FAIL'


def test_source_validity_does_not_create_unplanned_clears_in_four_full_stages():
    m,p,l,h,a=fixture();payload=l[0]['body']
    assert (m.instant(payload['endsAt'])-m.instant(payload['startsAt'])).total_seconds()>4*(1140+120)


@pytest.mark.parametrize('mutation', ['stale-source-audit','future-persistence-audit','future-parent-start',
    'parent-after-snapshot','source-after-snapshot','future-child-audit','delayed-ack'])
def test_relevant_evidence_is_causal_and_inside_completed_snapshot(mutation):
    if mutation=='future-child-audit':m,p,l,h,a=parent_fixture()
    elif mutation in ('stale-source-audit','source-after-snapshot','delayed-ack'):m,p,l,h,a=fixture()
    else:m,p,l,h,a=synthetic_fixture()
    end=m.stamp(START+timedelta(seconds=94))
    if mutation=='stale-source-audit':a[0]['created_at']='2020-01-01T00:00:00Z'
    elif mutation in ('future-persistence-audit','future-child-audit'):a[-1]['created_at']='2030-01-01T00:00:00Z'
    elif mutation=='future-parent-start':h[-1]['startsAt']='2030-01-01T00:00:00Z'
    elif mutation=='parent-after-snapshot':
        h[-1]['created_at']=h[-1]['startsAt']=a[0]['created_at']=m.stamp(START+timedelta(seconds=110))
    elif mutation=='source-after-snapshot':
        h[0]['created_at']=m.stamp(START+timedelta(seconds=91));l[0]['ended_wall']=m.stamp(START+timedelta(seconds=.1));end=m.stamp(START+timedelta(seconds=90.5))
        p['limits']['source_creation_seconds']=120
    elif mutation=='delayed-ack':l[0]['ended_wall']='2030-01-01T00:00:00Z';end='2030-01-01T00:01:34Z'
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=end)['verdict']=='FAIL'


def test_distinct_new_sources_cannot_reuse_one_native_identity_or_audit():
    m,p,l,h,a=fixture();event={**p['events'][0],'seq':1,'at_seconds':3,'source_id':'test-two','site':'ordinary-1','alertname':'MixedOrdinary_test_1'}
    p['events'].append(event);p['total']=2;l.append(m.record_exchange(p,event,START+timedelta(seconds=3),200,b'{"status":"ok"}',[REV],START+timedelta(seconds=3.1)))
    row=deepcopy(h[0]);row.update(id='h-two',fingerprint='oscar-two',alertname=event['alertname'],startsAt=l[1]['body']['startsAt'],created_at=m.stamp(START+timedelta(seconds=4)))
    row['labels']=[{'Label':k,'Value':v} for k,v in {**l[1]['body']['labels'],'am_fingerprint':'native-one','oscar_fingerprint':'oscar-two'}.items()];h.append(row);a[0]['created_at']=row['created_at']
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


@pytest.mark.parametrize('member', [0,1])
def test_parent_registration_and_child_link_cannot_be_suppressed(member):
    m,p,l,h,a=parent_fixture();a[member]['outcome']='suppressed_global'
    if member==1:a[member]['reason']='global_suppression'
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


@pytest.mark.parametrize('window', [None,'','w'*65])
def test_synthetic_parent_requires_nonempty_bounded_window(window):
    m,p,l,h,a=synthetic_fixture();a[0]['window_id']=window
    for item in h[-1]['labels']:
        if item['Label']=='oscar_correlation_window_id':item['Value']=window if window is not None else ''
        if item['Label']=='correlation_parent_identity':item['Value']=m.showcase.parent_identity(7,window)
    if window is None:h[-1]['labels']=[x for x in h[-1]['labels'] if x['Label']!='oscar_correlation_window_id']
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


@pytest.mark.parametrize('mutation',['parent-received','parent-occurrences','parent-label-severity','parent-current-severity',
    'source-current-severity','ordinary-rule-metadata','ordinary-role-metadata','parent-reuses-native'])
def test_physical_state_representations_and_native_identity_agree(mutation):
    if mutation.startswith('source') or mutation.startswith('ordinary'):m,p,l,h,a=fixture()
    else:m,p,l,h,a=synthetic_fixture()
    if mutation=='parent-received':h[-1]['received']=99
    elif mutation=='parent-occurrences':h[-1]['occurrences']=99
    elif mutation=='parent-label-severity':h[-1]['labels'].append({'Label':'severity','Value':'warning'})
    elif mutation=='parent-current-severity':h[-1]['severity']='warning'
    elif mutation=='source-current-severity':h[0]['severity']='critical'
    elif mutation=='ordinary-rule-metadata':h[0]['labels'].append({'Label':'oscar_correlation_rule_id','Value':'99'})
    elif mutation=='ordinary-role-metadata':h[0]['labels'].append({'Label':'oscar_correlation_role','Value':'child'})
    elif mutation=='parent-reuses-native':
        h[-1]['fingerprint']='native-one';a[0]['parent_fingerprint']='native-one'
        for item in h[-1]['labels']:
            if item['Label'] in ('am_fingerprint','oscar_fingerprint'):item['Value']='native-one'
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


@pytest.mark.parametrize('field,value',[('id',None),('id',''),('id',' '),('id','x'*37),('id','x'*65),('fingerprint',None),('fingerprint',''),('fingerprint',' '),('fingerprint','x'*51),('fingerprint','x'*129)])
def test_physical_history_identity_is_nonempty_bounded_string(field,value):
    m,p,l,h,a=fixture();h[0][field]=value
    if field=='fingerprint':
        next(x for x in h[0]['labels'] if x['Label']=='oscar_fingerprint')['Value']=value
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


@pytest.mark.parametrize('identity',[None,0,-1,True,'1'])
def test_audit_id_is_positive_non_boolean_integer(identity):
    m,p,l,h,a=fixture();a[0]['id']=identity
    assert m.evaluate(p,l,h,a,REV,snapshot_completed_at=m.stamp(START+timedelta(seconds=94)))['verdict']=='FAIL'


def test_body_uses_db_milliseconds_but_exchange_wall_keeps_actual_microseconds():
    m,p,l,h,a=fixture();actual=START.replace(microsecond=123456);p['start_at']=m.stamp(actual)
    event=p['events'][0];payload=m.body(event,p,actual)
    assert m.instant(payload['startsAt']).microsecond==123000
    assert m.instant(payload['endsAt']).microsecond==123000
    assert (m.instant(payload['endsAt'])-m.instant(payload['startsAt'])).total_seconds()==7200
    row=m.record_exchange(p,event,actual,200,b'{"status":"ok"}',[REV],actual+timedelta(seconds=.1))
    assert row['wall']==m.stamp(actual) and m.instant(row['wall']).microsecond==123456
    assert row['body']==payload
    assert base64.b64decode(row['request_body_base64'])==m.canonical(payload)
    h[0]['startsAt']=m.stamp(actual.replace(microsecond=123000))
    h[0]['created_at']=m.stamp((actual+timedelta(seconds=1)).replace(microsecond=123000))
    a[0]['created_at']=h[0]['created_at']
    assert m.validate_inputs(p,[row],REV)==[]
    assert m.evaluate(p,[row],h,a,REV,snapshot_completed_at=m.stamp(actual+timedelta(seconds=94)))['verdict']=='PASS'


@pytest.mark.parametrize('parent_offset_ms,created_offset_ms,audit_offset_ms,passed', [
    (0, 5, 1, True),
    (0, 0, 0, True),
    (-1, 5, 1, False),
    (0, 5, -1, False),
    (0, 90001, 1, False),
])
def test_persistence_event_due_preserves_exact_transport_latency(
        parent_offset_ms, created_offset_ms, audit_offset_ms, passed):
    m, p, ledger, histories, audits = synthetic_fixture()
    actual = START + timedelta(microseconds=987)
    ledger = [m.record_exchange(p, p['events'][0], actual, 200, b'{"status":"ok"}', [REV],
                               actual + timedelta(seconds=.1))]
    histories[0]['startsAt'] = ledger[0]['body']['startsAt']
    event_due = m.instant(ledger[0]['body']['startsAt']) + timedelta(seconds=30)
    histories[-1]['startsAt'] = m.stamp(event_due + timedelta(milliseconds=parent_offset_ms))
    created = event_due + timedelta(milliseconds=created_offset_ms)
    histories[-1]['created_at'] = m.stamp(created)
    audits[0]['created_at'] = m.stamp(event_due + timedelta(milliseconds=audit_offset_ms))
    report = m.evaluate(p, ledger, histories, audits, REV,
                        snapshot_completed_at=m.stamp(START + timedelta(seconds=120.05)))
    assert (report['verdict'] == 'PASS') is passed, report
    assert report['parent_after_eligibility_seconds']['p50'] == (created - actual - timedelta(seconds=30)).total_seconds()
    if created_offset_ms == 90001:
        assert any('parent deadline differs' in error for error in report['errors'])
