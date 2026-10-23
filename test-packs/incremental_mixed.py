#!/usr/bin/env python3
"""Portable offline mixed-load plans and physical-evidence verification; no service I/O."""
import argparse
import base64
from bisect import bisect_left
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone
import hashlib
import json
import math
from pathlib import Path

import build_showcase as build
import run_showcase as showcase

DEFAULT_LIMITS = {'input_lateness_seconds':2, 'ack_seconds':20, 'sender_completion_slack_seconds':22, 'source_creation_seconds':90,
                  'parent_after_eligibility_seconds':90, 'observation_after_sender_seconds':90, 'snapshot_after_sender_seconds':120}


def stamp(value):
    return value.astimezone(timezone.utc).isoformat().replace('+00:00','Z')


def instant(value):
    parsed=datetime.fromisoformat(value.replace('Z','+00:00'))
    # SQL DATETIME exports and the existing oracle use UTC without a suffix.
    if parsed.tzinfo is None: parsed=parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc)


def canonical(value):
    return json.dumps(value,sort_keys=True,separators=(',',':'),allow_nan=False).encode()


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def plan_digest(plan):
    return digest(canonical(plan))


def write_plan(path,plan):
    raw=canonical(plan)
    with Path(path).open('xb') as output: output.write(raw)
    return digest(raw)


def read_plan(path,pin):
    raw=Path(path).read_bytes()
    if digest(raw)!=pin: raise ValueError('plan pin differs')
    return json.loads(raw)


def make_plan(run,rate,start_at,profile='full',rules=None):
    if rate not in (10,15,20,25) or not isinstance(run,str) or not run.strip(): raise ValueError('fixed rate and nonempty run required')
    if isinstance(start_at,str): start_at=instant(start_at)
    if start_at.tzinfo is None: raise ValueError('timezone required')
    ramp,hold={'full':(120,900),'pilot':(10,60)}[profile]
    duration=2*ramp+hold;times=[];carry=0
    for second in range(duration):
        factor=.2+.8*second/ramp if second<ramp else 1 if second<ramp+hold else 1-.8*(second-ramp-hold)/ramp
        carry+=rate*factor;count=math.floor(carry+1e-9);carry-=count
        times.extend(second+i/count for i in range(count))
    packets=int(len(times)*.3)//31;occupied={};groups=[];epoch=start_at.timestamp()
    for cycle in range(packets):
        for number,case in enumerate(build.scenario()['cases']):
            site=f'g{cycle:04d}-{case["id"]}';target=cycle*max(0,duration-75)/max(1,packets)+number*.18
            while True:
                chosen=[];due=target
                for item in case['inputs']:
                    slot=bisect_left(times,due)
                    while slot in occupied or slot in chosen: slot+=1
                    if slot>=len(times): raise ValueError('scenario does not fit finite rate slots')
                    chosen.append(slot);due=times[slot]+3.0000001
                if len({int(epoch+times[s])//60 for s in chosen} | {int(epoch+times[chosen[-1]]+DEFAULT_LIMITS['input_lateness_seconds'])//60})==1: break
                target=(int(epoch+times[chosen[0]])//60+1)*60+1-epoch
            sources=[]
            for index,(item,slot) in enumerate(zip(case['inputs'],chosen)):
                source=f'{run}-{site}-{index}';sources.append(source)
                occupied[slot]={'site':site,'source_id':source,'alertname':item['alertname'],
                    'device':item['device'],'oscar_source':item['oscar_source'],'case':case['id'],'group':site}
            groups.append({'site':site,'case':case['id'],'pattern':case['pattern'],'positive':case['polarity']=='positive',
                'sources':sources,'expected_parents':case['expected_parent_history'], 'expected_links':case['expected_existing_parent_links'],
                'parent_alertname':build.alertname(case['pattern'],'synthetic_parent'),'expected_source_state':'firing','expected_parent_state':'firing'})
    events=[]
    for seq,due in enumerate(times):
        event=occupied.get(seq,{'site':f'ordinary-{seq}','source_id':f'{run}-ordinary-{seq}',
            'alertname':f'MixedOrdinary_{run}_{seq}','device':'ordinary','oscar_source':'api','case':'ordinary','group':None})
        events.append({'seq':seq,'at_seconds':due,**event})
    rule_map={r['pattern']:{'id':r['id'],'name':r['name']} for r in rules or [] if r.get('name','').startswith('corrtest-showcase-v2-')}
    return {'format':'corrtest-incremental-mixed-v1','run':run,'rate':rate,'profile':profile,'start_at':stamp(start_at),
        'phases':{'ramp_up':[0,ramp],'hold':[ramp,ramp+hold],'ramp_down':[ramp+hold,duration]},
        'duration_seconds':duration,'source_validity_seconds':7200,'events':events,'groups':groups,'total':len(events),'packets':packets,
        'correlation_inputs':len(occupied),'ordinary_inputs':len(events)-len(occupied),
        'synthetic_parents':packets*6,'existing_parent_links':packets,'rules':rule_map,'limits':DEFAULT_LIMITS.copy()}


def body(event,plan,actual_now):
    return {'labels':{'alertname':event['alertname'],'qualification_pack':build.PROFILE,'qualification_run':plan['run'],
        'site':event['site'],'source_id':event['source_id'],'severity':'warning','device':event['device'],'oscar_source':event['oscar_source']},
        'startsAt':stamp(actual_now),'endsAt':stamp(actual_now+timedelta(seconds=plan.get('source_validity_seconds',7200)))}


def record_exchange(plan,event,actual_now,response_status,response_bytes,revisions,ended_at):
    payload=body(event,plan,actual_now);request=canonical(payload)
    return {'seq':event['seq'],'case':event['case'],'group':event['group'],'body':payload,'wall':stamp(actual_now),
        'ended_wall':stamp(ended_at),'status':response_status,'profile_revisions':revisions,
        'request_body_base64':base64.b64encode(request).decode(),'request_body_sha256':digest(request),
        'response_body_base64':base64.b64encode(response_bytes).decode(),'response_body_sha256':digest(response_bytes)}


def validate_inputs(plan,ledger,revision):
    errors=[];expected={e['seq']:e for e in plan['events']};seen={};start=instant(plan['start_at']);last_slot=max((e['at_seconds'] for e in expected.values()),default=0)
    if not isinstance(revision,str) or len(revision)!=64 or any(c not in '0123456789abcdef' for c in revision): errors.append('profile revision pin missing')
    if len(expected)!=plan['total'] or len(ledger)!=plan['total']: errors.append('input inventory differs')
    for row in ledger:
        try:
            seq=row['seq'];event=expected[seq]
            if seq in seen: raise ValueError('duplicate sender sequence')
            now=instant(row['wall']);seen[seq]=row
            request=base64.b64decode(row['request_body_base64'],validate=True);response=base64.b64decode(row['response_body_base64'],validate=True)
            if digest(request)!=row['request_body_sha256'] or digest(response)!=row['response_body_sha256']: raise ValueError('HTTP byte hash differs')
            if showcase.strict(request)!=row['body'] or row['body']!=body(event,plan,now): raise ValueError('input payload differs from frozen plan')
            if row['case']!=event['case'] or row['group']!=event['group']: raise ValueError('input case binding differs')
            lag=(now-start).total_seconds()-event['at_seconds']
            if not 0<=lag<=plan['limits']['input_lateness_seconds']: raise ValueError('input schedule deadline differs')
            ended=instant(row['ended_wall'])
            if not 0<=(ended-now).total_seconds()<=plan['limits']['ack_seconds']: raise ValueError('response ACK deadline differs')
            if (ended-start).total_seconds()>last_slot+plan['limits']['sender_completion_slack_seconds']: raise ValueError('sender completion deadline differs')
            if row['status']!=200 or showcase.strict(response)!={'status':'ok'} or row['profile_revisions']!=[revision]: raise ValueError('exact receiver ACK missing')
            if 'accepted' in row and row['accepted'] is not True: raise ValueError('accepted flag contradicts ACK')
        except (KeyError,TypeError,ValueError) as exc: errors.append('input: '+str(exc))
    if set(seen)!=set(expected): errors.append('sender sequence inventory differs')
    by_source={e['source_id']:e['seq'] for e in plan['events']}
    for group in plan['groups']:
        try:
            times=[instant(seen[by_source[s]]['wall']) for s in group['sources']]
            if any((b-a).total_seconds()<3 for a,b in zip(times,times[1:])): errors.append(group['site']+': actual source order/spacing differs')
            if len({int(t.timestamp())//60 for t in times})!=1: errors.append(group['site']+': actual minute window straddles')
        except KeyError: errors.append(group['site']+': source input missing')
    return errors


def evaluate(plan,ledger,histories,audits,revision,*,snapshot_completed_at=None):
    input_errors=validate_inputs(plan,ledger,revision);errors=list(input_errors)
    def check(condition,message):
        if not condition: errors.append(message)
    expected={x['body']['labels']['source_id']:x for x in ledger};sources={};parents=defaultdict(list);ids=set();fps=set();native_fps=set();by_fp=defaultdict(list)
    source_latency=[];parent_latency=[];pairs=set();logical_audits=set();repeats=0
    event_by_source={e['source_id']:e for e in plan['events']}
    source_states={s:g.get('expected_source_state','firing') for g in plan['groups'] for s in g['sources']}
    try:
        snapshot_end=instant(snapshot_completed_at) if snapshot_completed_at else None
        audit_ids=set()
        for audit in audits:
            check(type(audit['id']) is int and audit['id']>0,'invalid audit identity')
            check(audit['id'] not in audit_ids,'duplicate audit id');audit_ids.add(audit['id'])
            by_fp[audit['alert_fingerprint']].append(audit)
            logical=canonical({k:v for k,v in audit.items() if k not in ('id','created_at')})
            repeats+=logical in logical_audits;logical_audits.add(logical)
        for row in histories:
            labels=showcase.pairs(row.get('labels'),'Label','Value')
            if labels.get('qualification_run')!=plan['run']: continue
            check(type(row['id']) is str and bool(row['id'].strip()) and len(row['id'])<=36 and type(row['fingerprint']) is str and bool(row['fingerprint'].strip()) and len(row['fingerprint'])<=50,'invalid physical History identity')
            check(row['id'] not in ids and row['fingerprint'] not in fps,'duplicate physical History identity');ids.add(row['id']);fps.add(row['fingerprint'])
            if labels.get('oscar_synthetic_source')=='correlator': parents[labels.get('site')].append((row,labels));continue
            source=labels.get('source_id');check(source in expected and source not in sources,'unexpected/duplicate physical source')
            if source not in expected: continue
            original=expected[source];sources[source]=(row,labels)
            for key,want in original['body']['labels'].items():
                actual=row.get('original_severity',row.get('severity')) if key=='severity' else row.get('alertname') if key=='alertname' else labels.get(key)
                check(actual==want and labels.get(key,want)==want,source+': physical source label differs '+key)
            check(instant(row['startsAt'])==instant(original['body']['startsAt']),source+': source start differs')
            state=event_by_source[source].get('expected_state',source_states.get(source,'firing'))
            check(row['status']==state and row['received']==1 and row.get('occurrences',1)==1,source+': source state/occurrence differs')
            check('severity' not in row or row['severity']==event_by_source[source].get('expected_severity',original['body']['labels']['severity']),source+': conflicting physical source severity')
            check(bool(labels.get('am_fingerprint')) and labels.get('oscar_fingerprint')==row['fingerprint'],source+': Native/OSCAR linkage differs')
            native=labels.get('am_fingerprint');check(native not in native_fps,source+': duplicate Native source identity');native_fps.add(native)
            created=instant(row['created_at']);delay=(created-instant(original['wall'])).total_seconds();source_latency.append(delay)
            check(snapshot_end is None or created<=snapshot_end,source+': source after completed snapshot')
            for decision in by_fp[native]:
                decided=instant(decision['created_at'])
                check(instant(original['wall'])<=decided and (snapshot_end is None or decided<=snapshot_end),source+': audit outside input/snapshot interval')
            check(0<=delay<=plan['limits']['source_creation_seconds'],source+': source creation deadline differs')
            check(bool(by_fp[labels.get('am_fingerprint')]),source+': source audit missing')
        check(set(sources)==set(expected),'physical source inventory differs')
        check(set(parents)<={g['site'] for g in plan['groups']},'unexpected physical parent group')
        for group in plan['groups']:
            site=group['site'];pattern=group['pattern'];physical=parents.get(site,[])
            check(len(physical)==group['expected_parents'],site+': physical parent count differs')
            members={s:sources[s] for s in group['sources'] if s in sources}
            check(len(members)==len(group['sources']),site+': physical source missing')
            decisions=[a for row,labels in members.values() for a in by_fp[labels['am_fingerprint']]]
            unmatched=group['case']=='persistence-negative';rule=plan['rules'].get(pattern)
            if not unmatched: check(rule is not None and type(rule.get('id')) is int and rule['id']>0,site+': frozen loaded rule missing')
            for row,labels in members.values():
                own=by_fp[labels['am_fingerprint']]
                for decision in own:
                    if unmatched: check(all(k in decision and decision[k] is None for k in ('rule_id','rule_name','pattern','window_id','parent_fingerprint')) and decision['outcome']=='released_no_trigger' and decision['reason']=='no_matching_rules',site+': unmatched decision differs')
                    elif rule:
                        check(decision['rule_id']==rule['id'] and decision['rule_name']==rule['name'] and decision['pattern']==pattern,site+': selected rule differs')
                        check(decision['outcome'] in ('enriched','parent_emitted','released_no_trigger'),site+': disallowed matched decision outcome')
            trigger_rows=[a for a in decisions if a['outcome']=='parent_emitted']
            for trigger in trigger_rows:
                check(type(trigger.get('window_id')) is str and 0<len(trigger['window_id'])<=64 and type(trigger.get('parent_fingerprint')) is str and 0<len(trigger['parent_fingerprint'])<=128,site+': invalid trigger window/parent identity')
            triggers={(a['rule_id'],a['window_id'],a['parent_fingerprint']) for a in trigger_rows}
            check(len(triggers)==group['expected_parents'],site+': distinct parent trigger count differs')
            for row,labels in physical:
                matching=[a for a in decisions if a['outcome']=='parent_emitted' and a['parent_fingerprint']==row['fingerprint'] and a['rule_id']==(rule or {}).get('id') and a['window_id']==labels.get('oscar_correlation_window_id')]
                check(bool(matching),site+': physical parent trigger attribution differs')
                if not matching: continue
                trigger=matching[0]
                want={'qualification_pack':build.PROFILE,'qualification_run':plan['run'],'site':site,'oscar_correlation_rule_id':str(rule['id']),'oscar_correlation_rule_name':rule['name'],'am_fingerprint':row['fingerprint'],'oscar_fingerprint':row['fingerprint'],'correlation_parent_identity':showcase.parent_identity(rule['id'],trigger['window_id'])}
                check(all(labels.get(k)==v for k,v in want.items()) and labels.get('am_fingerprint') not in native_fps,site+': physical parent identity differs')
                check(row.get('received')==1 and row.get('occurrences',1)==1,site+': parent occurrence differs')
                check(all(key not in row or row[key]==value for key,value in [('severity',group.get('expected_parent_severity','critical')),('original_severity','critical')]) and labels.get('severity','critical')=='critical' and labels.get('alertname',row['alertname'])==row['alertname'],site+': conflicting physical parent severity/name')
                check(row['alertname']==group['parent_alertname'] and row.get('original_severity',row.get('severity'))=='critical' and row['status']==group.get('expected_parent_state','firing'),site+': parent state/name/severity differs')
                eligibility=max(instant(expected[s]['wall']) for s in group['sources'])
                if pattern=='persistence': eligibility+=timedelta(seconds=group.get('duration',30))
                created=instant(row['created_at']);parent_start=instant(row['startsAt'])
                check(eligibility<=parent_start<=created and (snapshot_end is None or created<=snapshot_end),site+': parent outside eligibility/snapshot interval')
                delay=(created-eligibility).total_seconds();parent_latency.append(delay)
                check(0<=delay<=plan['limits']['parent_after_eligibility_seconds'],site+': parent deadline differs')
                if pattern=='persistence': check(instant(row['startsAt'])>=eligibility and all(instant(a['created_at'])>=eligibility for a in matching),site+': premature Persistence parent')
            if pattern=='parent_child':
                existing=[(r,l) for r,l in members.values() if r['alertname'].endswith('_parent')];children=[(r,l) for r,l in members.values() if r['alertname'].endswith('_child')]
                if group['expected_links']:
                    check(len(existing)==len(children)==1,site+': parent-child members differ')
                    if existing and children:
                        parent_fp=existing[0][1]['am_fingerprint'];child_fp=children[0][1]['am_fingerprint']
                        check(all(a['outcome']=='enriched' and a['reason']=='parent_registered' and a['parent_fingerprint']==parent_fp for a in by_fp[parent_fp]),site+': existing parent registration differs')
                        check(all(a['outcome']=='enriched' and a['reason'].startswith('parent_child_active_parent_present tag_only') and a['parent_fingerprint']==parent_fp for a in by_fp[child_fp]),site+': existing child decision differs')
                        links={(a['alert_fingerprint'],a['parent_fingerprint']) for a in decisions if a['parent_fingerprint'] and a['reason']!='parent_registered'}
                        check(links=={(child_fp,parent_fp)},site+': distinct parent-child audit association differs')
                        for role,(row,labels) in [('parent',existing[0]),('child',children[0])]:
                            want={'oscar_correlation_rule_id':str((rule or {}).get('id')),'oscar_correlation_rule_name':(rule or {}).get('name'),'oscar_correlation_pattern':'parent_child','oscar_correlation_role':role,'oscar_correlation_parent_fingerprint':parent_fp}
                            check(all(labels.get(k)==v for k,v in want.items()),site+': physical parent-child link differs')
                            check(bool(labels.get('oscar_correlation_window_id')) and any(a['window_id']==labels.get('oscar_correlation_window_id') and a['parent_fingerprint']==parent_fp for a in by_fp[labels['am_fingerprint']]),site+': own physical parent-child window differs')
                        pairs.add((child_fp,parent_fp))
                else: check(all(not labels.get('oscar_correlation_parent_fingerprint') for row,labels in members.values()) and all(not a['parent_fingerprint'] for a in decisions),site+': negative physical parent-child association')
        for item in ledger:
            source=item['body']['labels']['source_id']
            if item['case']=='ordinary' and source in sources:
                row,labels=sources[source]
                check(all(key not in labels for key in ('oscar_correlation_rule_id','oscar_correlation_rule_name','oscar_correlation_pattern','oscar_correlation_role','oscar_correlation_window_id','oscar_correlation_parent_fingerprint')) and all(all(k in a and a[k] is None for k in ('rule_id','rule_name','pattern','window_id','parent_fingerprint')) and a['outcome']=='released_no_trigger' and a['reason']=='no_matching_rules' for a in by_fp[labels['am_fingerprint']]),source+': ordinary source incorrectly correlated')
        if snapshot_completed_at:
            last=max(instant(x['ended_wall']) for x in ledger)
            absolute_end=instant(plan['start_at'])+timedelta(seconds=max(e['at_seconds'] for e in plan['events'])+plan['limits']['sender_completion_slack_seconds']+plan['limits']['snapshot_after_sender_seconds'])
            check(snapshot_end<=absolute_end,'absolute stage snapshot deadline differs')
            check(plan['limits']['observation_after_sender_seconds']<=(instant(snapshot_completed_at)-last).total_seconds()<=plan['limits']['snapshot_after_sender_seconds'],'physical snapshot observation/deadline differs')
        else: errors.append('physical snapshot completion missing')
    except (KeyError,TypeError,ValueError) as exc: errors.append('malformed evidence: '+str(exc))
    invalid_vector=any(any(reason in error for reason in ('schedule deadline','order/spacing','minute window','payload differs','case binding')) for error in input_errors)
    return {'verdict':'INCONCLUSIVE' if invalid_vector or not snapshot_completed_at else 'FAIL' if errors else 'PASS','errors':errors,
        'physical_sources':len(sources),'physical_synthetic_parents':sum(map(len,parents.values())),
        'existing_parent_links':len(pairs),'raw_audit_rows':len(audits),'repeat_audit_rows':repeats,
        'source_creation_seconds':distribution(source_latency),'parent_after_eligibility_seconds':distribution(parent_latency)}


def distribution(values):
    ordered=sorted(values)
    return {name:ordered[max(0,math.ceil(len(ordered)*p)-1)] if ordered else None for name,p in [('p50',.5),('p95',.95),('p99',.99),('max',1)]}


def summarize_samples(plan,ledger,samples):
    result={};start=instant(plan['start_at']);events={e['seq']:e for e in plan['events']}
    for name,(lower,upper) in plan['phases'].items():
        rows=[x for x in ledger if lower<=(instant(x['wall'])-start).total_seconds()<upper]
        observed=[x for x in samples if lower<=x['at_seconds']<upper];queues={}
        for sample in observed:
            for key,value in sample.get('queues',{}).items(): queues[key]=max(queues.get(key,0),value)
        result[name]={'offered':len(rows),'offered_per_second':len(rows)/(upper-lower),'samples':len(observed),
            'sample_errors':[x['errors'] for x in observed if x.get('errors')],'sampled_queue_peaks':queues,
            'ingress_response_seconds':distribution([(instant(x['ended_wall'])-instant(x['wall'])).total_seconds() for x in rows]),
            'schedule_lag_seconds':distribution([(instant(x['wall'])-start).total_seconds()-events[x['seq']]['at_seconds'] for x in rows])}
    return result


def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='command',required=True)
    create=sub.add_parser('plan');create.add_argument('--run',required=True);create.add_argument('--rate',type=int,choices=[10,15,20,25],required=True);create.add_argument('--start-at',required=True);create.add_argument('--profile',choices=['full','pilot'],default='full');create.add_argument('--rules',type=Path);create.add_argument('--output',type=Path,required=True)
    verify=sub.add_parser('verify');verify.add_argument('--plan',type=Path,required=True);verify.add_argument('--plan-sha256',required=True);verify.add_argument('--ledger',type=Path,required=True);verify.add_argument('--history',type=Path,required=True);verify.add_argument('--audit',type=Path,required=True);verify.add_argument('--revision',required=True);verify.add_argument('--snapshot-completed-at')
    args=parser.parse_args()
    if args.command=='plan':
        plan=make_plan(args.run,args.rate,args.start_at,args.profile,json.loads(args.rules.read_text()) if args.rules else None);print(write_plan(args.output,plan));return 0
    plan=read_plan(args.plan,args.plan_sha256);ledger=[json.loads(x) for x in args.ledger.read_text().splitlines()]
    report=evaluate(plan,ledger,json.loads(args.history.read_text()),json.loads(args.audit.read_text()),args.revision,snapshot_completed_at=args.snapshot_completed_at)
    print(json.dumps(report,sort_keys=True));return 0 if report['verdict']=='PASS' else 1


if __name__=='__main__':raise SystemExit(main())
