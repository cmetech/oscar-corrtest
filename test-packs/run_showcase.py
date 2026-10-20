#!/usr/bin/env python3
"""Finite retained receiver/native/History showcase; no installation or cleanup.

This is additive CorrTest pack tooling, not an extension of the Go Flood CLI.
The independent verify command repeats semantic checks from retained responses.
"""
import argparse
import base64
from datetime import datetime, timezone, timedelta
import hashlib
import io
import json
import os
from pathlib import Path
import re
import tarfile
import time
from urllib.parse import urlencode, urlsplit
from uuid import uuid4

import build_showcase as build
import install_operational as existing

common = build.common
require = existing.require
RULES = '/api/v1/correlation_rules'
HISTORY = '/api/v1/alerts/history'
ACTIVE = '/api/v1/alerts/active-alerts'
AUDIT = RULES + '/audit'
POST = '/webhook/corrtest_showcase_v2'
LIMITS = ['ordinary patterns only; no enhanced/source-recovery qualification',
          'operator-owned isolated target and controlled destinations required throughout run and export',
          'point-in-time readback cannot detect change/use/restore between requests',
          'History parent is not external notification-provider delivery',
          'no source clear, no retry, no rule deletion; retained responsibility',
          'one finite case per invocation; no throughput or capacity claim']


def stamp(value): return value.astimezone(timezone.utc).isoformat().replace('+00:00', 'Z')
def instant(value):
    require(type(value) is str and len(value) <= 64, 'invalid timestamp')
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    return parsed.replace(tzinfo=timezone.utc) if parsed.tzinfo is None else parsed.astimezone(timezone.utc)
def parent_identity(rule_id, window):
    return hashlib.sha256(json.dumps([str(rule_id), window], separators=(',', ':')).encode()).hexdigest()[:32]


def strict(raw):
    value = common.strict_json(raw)
    def walk(item, depth=0):
        require(depth <= 24, 'JSON nesting bound')
        if isinstance(item, dict):
            require(len({key.casefold() for key in item}) == len(item), 'folded duplicate JSON key')
            for nested in item.values(): walk(nested, depth+1)
        elif isinstance(item, list):
            for nested in item: walk(nested, depth+1)
        elif isinstance(item, float): require(False, 'non-integral JSON number unsupported')
    walk(value)
    return value


def pack_identity(directory):
    """Read only bounded expected tar members; never extract or execute the pack."""
    raw = common.read(Path(directory) / 'corrtest_showcase-0.2.0.tar.gz')
    prefix = build.PACK_ID + '/'
    path = 'conf/mappings/' + build.PROFILE + '.json'
    expected = {prefix+'pack.yml', prefix+path}
    files = {}
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        for index, member in enumerate(archive):
            require(index < 8, 'pack member count bound')
            if member.isdir():
                require(member.name in {build.PACK_ID,prefix+'conf',prefix+'conf/mappings'}, 'foreign directory')
                continue
            require(member.isfile() and member.name in expected and member.name not in files and 0<member.size<=common.MAX_BYTES, 'invalid pack member')
            files[member.name] = archive.extractfile(member).read(common.MAX_BYTES+1)
            require(len(files[member.name])==member.size, 'pack member size mismatch')
    require(set(files)==expected, 'incomplete pack')
    require(strict(files[prefix+path])==[build.profile()], 'packed profile differs')
    state = {**build.profile(), 'comment':None,'additional_ref':None,
             'element':sorted(build.profile()['element'],key=lambda row:(row['key'],row['value'])),
             'metadata':[{'key':'source_pack','value':build.PACK_ID}]}
    return 'sha256:'+common.digest(files[prefix+'pack.yml']+path.encode()+files[prefix+path]), state


class Journal(existing.Journal):
    def __init__(self, directory, tokens):
        self.tokens = tokens
        super().__init__(directory, tokens[0])
    def redact(self, value):
        result = super().redact(value)
        for token in self.tokens[1:]:
            if isinstance(result,str): result=result.replace(token,'[redacted]')
        return result


class Endpoint(existing.API):
    """Reuse verified TLS, credential transport and zero retries from installer."""
    def __init__(self,*args,now,monotonic,**kwargs):
        super().__init__(*args,**kwargs)
        self.now,self.monotonic=now,monotonic
    def timing(self):
        value={'observed_at':stamp(self.now()),'monotonic_ns':int(self.monotonic()*1_000_000_000)}
        previous=getattr(self.journal,'last_timing',None)
        if previous is not None:
            require(instant(value['observed_at'])>=instant(previous['observed_at']) and value['monotonic_ns']>=previous['monotonic_ns'],'clock regressed during HTTP operation')
        self.journal.last_timing=value
        return value
    def exchange(self, method, path, body=None, revision=None):
        base_path = path.split('?',1)[0]
        allowed = {RULES,RULES+'/runtime','/api/v1/packs',HISTORY,ACTIVE,AUDIT}
        require((method=='GET' and (base_path in allowed or re.fullmatch(r'/api/v1/packs/corrtest_showcase/configuration/items/mapping:[0-9a-f]{64}',base_path))) or (method=='POST' and path==POST and revision), 'fixed showcase endpoint required')
        raw_body=common.canonical(body) if body is not None else None
        self.last_request_timing=self.timing()
        sequence=self.journal.before('showcase-http',{'method':method,'base':self.target,'path':path,
            'body_base64':base64.b64encode(raw_body).decode() if raw_body is not None else None,
            'body_sha256':common.digest(raw_body) if raw_body is not None else None,
            'profile_revision':revision,**self.last_request_timing})
        kwargs={'content':raw_body,'headers':{'Content-Type':'application/json','If-Match':'"'+revision+'"'}} if raw_body is not None else {}
        with self.client.stream(method,self.target+path,timeout=min(20,self.remaining()),**kwargs) as response:
            data=bytearray()
            for chunk in response.iter_bytes():
                require(len(data)+len(chunk)<=common.MAX_BYTES,'response byte bound')
                data.extend(chunk); self.remaining()
            raw=bytes(data)
            outcome={'status':response.status_code,'body_base64':base64.b64encode(raw).decode(),
                     'body_sha256':common.digest(raw),'profile_revisions':response.headers.get_list('X-Oscar-Profile-Revision'),**self.timing()}
            self.journal.after(sequence,outcome)
            self.last_response_timing={key:outcome[key] for key in ['observed_at','monotonic_ns']}
            require(response.status_code==200,'HTTP status '+str(response.status_code))
            parsed=strict(raw)
            require(type(parsed) is dict,'JSON object response required')
            if method=='POST': require(parsed=={'status':'ok'} and outcome['profile_revisions']==[revision],'forwarding/revision acknowledgment missing')
            return parsed


def retained_v1():
    return {'name':'corrtest-operational-v1-flood','pattern':'flood','window_seconds':60,
            'group_by_labels':['qualification_run','site'],'match_criteria':{'match':{'alertname':'CorrTestOperationalFlood'},'min_count':5},
            'correlation_policy':None,'emit_spec':{'alertname':'CorrTestOperationalFloodParent'},'for_notifiers':None,
            'priority':100,'max_synthetic_per_minute':10,'enabled':True,
            'description':'Fixed owned ordinary initial-Flood audit qualification v1'}


def preflight(api,directory):
    rules=api.exchange('GET',RULES+'?'+urlencode({'page':1,'perPage':100,'column':'id','order':'asc'}))
    rows=rules.get('rows')
    require(type(rows) is list and len(rows)==8 and rules.get('total')==8 and rules.get('page')==1 and rules.get('perPage')==100,'exact eight retained rules required')
    expected={row['name']:{**row,'enabled':True} for row in build.rules()}
    ids=set(); names=set()
    for row in rows:
        require(type(row) is dict and existing.positive(row.get('id')) and row['id'] not in ids and row.get('name') not in names,'duplicate/invalid rule')
        ids.add(row['id']); names.add(row.get('name'))
        if row.get('name')=='corrtest-operational-v1-flood':
            retained={**retained_v1(),'enabled':row.get('enabled')}
            require(type(row.get('enabled')) is bool and all(row.get(key)==value for key,value in retained.items()),'retained v1 authored rule differs')
        else:
            want=expected.get(row.get('name')); require(want is not None and all(row.get(key)==value for key,value in want.items()),'showcase rule differs')
    require(names==set(expected)|{'corrtest-operational-v1-flood'},'rule inventory differs')
    runtime=api.exchange('GET',RULES+'/runtime')
    loaded=sorted(row['id'] for row in rows if row.get('enabled'))
    require(runtime.get('dispatch_enabled') is True and runtime.get('authoritative') is True,'ordinary authoritative dispatch-enabled runtime required')
    breaker=runtime.get('breaker',{}); enhanced=runtime.get('enhanced_correlation',{})
    require(runtime.get('operational') is True and breaker.get('state')=='healthy' and breaker.get('fresh') is True,'fresh operational dispatch breaker required')
    require(enhanced.get('production_enabled') is False,'enhanced production admission must remain disabled')
    require(runtime.get('load_status')=='loaded' and runtime.get('loaded_rule_ids')==loaded and runtime.get('loaded_total')==len(loaded)
            and all(type(runtime.get(key)) is int and runtime[key]==0 for key in ['loaded_omitted','rejected_total','rejected_omitted']) and runtime.get('rejected_rules')==[]
            and existing.positive(runtime.get('checked_at_ms')) and existing.positive(runtime.get('last_attempt_ms')) and type(runtime.get('loaded_at')) is str,'complete runtime inventory required')
    deps=runtime.get('dependencies',{})
    require(deps.get('status')=='ready' and all(deps.get(key) is True for key in ['nats','consumer','redis','lua_registered','timer_store']),'runtime dependencies unavailable')
    packs=api.exchange('GET','/api/v1/packs').get('packs')
    require(type(packs) is list and len(packs)<=256,'pack inventory missing')
    pack_hash,state=pack_identity(directory)
    found=[row for row in packs if row.get('pack_id')==build.PACK_ID]
    require(len(found)==1 and all(found[0].get(k)==v for k,v in {'pack_version':build.VERSION,'manifest_status':'valid','pack_content_hash':pack_hash}.items()),'installed pack differs')
    identity={'namespace':'webhook_profiles','name':build.PROFILE}
    item=api.exchange('GET','/api/v1/packs/corrtest_showcase/configuration/items/mapping:'+existing.canonical_hash(identity))
    require(item.get('state')=='up_to_date' and item.get('changed_fields')==[] and item.get('pack')==state and item.get('live')==state
            and all(item.get(k)==existing.canonical_hash(state) for k in ['pack_hash','live_hash','applied_hash']),'installed mapping differs')
    return {'rules':rules,'runtime':{key:runtime[key] for key in ['loaded_at','last_attempt_ms','loaded_rule_ids','dispatch_enabled','authoritative']},'pack':found[0],'mapping':item}


def pairs(rows,key,value):
    require(type(rows) is list,'label list required')
    result={}
    for row in rows:
        require(type(row) is dict and type(row.get(key)) is str and type(row.get(value)) is str and row[key] not in result,'duplicate/invalid label')
        require('value_truncated' not in row or row['value_truncated'] is False,'truncated evidence')
        require('value_length' not in row or type(row['value_length']) is int and row['value_length']==len(row[value]),'incomplete evidence')
        result[row[key]]=row[value]
    return result


def page(value,kind):
    rows_key,total_key,per_key=('rows','total','perPage') if kind=='audit' else ('records','total_records','per_page')
    rows=value.get(rows_key)
    require(type(rows) is list and len(rows)<=100 and type(value.get(total_key)) is int and value[total_key]==len(rows),'incomplete '+kind+' page')
    if kind!='active': require(value.get('page')==1 and value.get(per_key)==100,'page metadata differs')
    if kind!='audit': require(type(value.get('total_pages')) is int and value['total_pages']==int(bool(rows)),'page total differs')
    return rows


def filtered_path(path,alertname,run,start,end,active=False):
    items=[{'field':'alertname','operator':'equals','value':alertname}] if alertname else []
    if active: items += [{'field':'label:qualification_pack','operator':'equals','value':build.PROFILE},{'field':'label:qualification_run','operator':'equals','value':run}]
    values={'page':1,'perPage':100,'column':'fingerprint' if active else 'createdAt','order':'asc','filter':json.dumps({'logicOperator':'and','items':items},separators=(',',':'))}
    if active: values['show_children']='true'
    else: values.update(start_datetime=stamp(start),end_datetime=stamp(end))
    return path+'?'+urlencode(values)


def observe(api,case,run,start,end):
    active=api.exchange('GET',filtered_path(ACTIVE,None,run,start,end,True))
    capture=dict(api.last_request_timing)
    active_rows=page(active,'active')
    history={}
    names=sorted({item['alertname'] for item in case['inputs']}|{build.alertname(case['pattern'],'synthetic_parent')})
    for name in names: history[name]=api.exchange('GET',filtered_path(HISTORY,name,run,start,end))
    audits={}
    for row in active_rows:
        fp=row.get('fingerprint'); require(type(fp) is str and re.fullmatch('[0-9a-f]{16}',fp) and fp not in audits,'native fingerprint invalid/duplicate')
        audits[fp]=api.exchange('GET',AUDIT+'?'+urlencode({'fingerprint':fp,'page':1,'perPage':100}))
    return {'active':active,'history':history,'audit':audits,'capture':capture}


def evaluate(case,run,start,end,inputs,observed,rule):
    expected={body['labels']['source_id']:body for body in inputs}
    active={}; fingerprints=set()
    for row in page(observed['active'],'active'):
        labels=pairs(row.get('alert_labels'),'name','value'); source=labels.get('source_id'); fp=row.get('fingerprint')
        require(source in expected and source not in active and type(fp) is str and re.fullmatch('[0-9a-f]{16}',fp) and fp not in fingerprints,'unexpected native source')
        require(labels==expected[source]['labels'] and instant(row.get('starts_at'))==instant(expected[source]['startsAt']) and row.get('alertname')==labels['alertname'],'native source changed')
        active[source]=fp; fingerprints.add(fp)
    require(set(active)==set(expected),'native inputs missing')
    history={}; parent=[]; history_ids=set(); source_history_fingerprints=set()
    parent_name=build.alertname(case['pattern'],'synthetic_parent')
    names={item['alertname'] for item in case['inputs']}|{parent_name}
    require(set(observed['history'])==names,'history query set differs')
    for name,value in observed['history'].items():
        for row in page(value,'history'):
            labels=pairs(row.get('labels'),'Label','Value')
            require(row.get('alertname')==name,'foreign History query row')
            if labels.get('qualification_run')!=run: continue
            require(type(row.get('ID')) is str and re.fullmatch('[0-9a-f-]{36}',row['ID']) and row['ID'] not in history_ids,'duplicate History identity')
            history_ids.add(row['ID'])
            require(start<=instant(row.get('createdAt'))<=end,'History outside observation interval')
            if name==parent_name: parent.append((row,labels)); continue
            source=labels.get('source_id'); require(source in expected and source not in history,'unexpected source History')
            body=expected[source]
            require(row.get('status')=='firing' and instant(row.get('startsAt'))==instant(body['startsAt']),'source History state differs')
            for key,want in body['labels'].items():
                if key=='severity': require((key in labels or row.get('severity') is not None) and (key not in labels or labels[key]==want) and (row.get('severity') is None or row['severity']==want),'source severity changed')
                else: require(labels.get(key)==want,'source History label differs: '+key)
            require(labels.get('am_fingerprint')==active[source] and labels.get('oscar_fingerprint')==row.get('fingerprint') and row.get('fingerprint'),'History native link differs')
            require(row['fingerprint'] not in source_history_fingerprints,'duplicate source History fingerprint')
            source_history_fingerprints.add(row['fingerprint']); history[source]=row
    require(set(history)==set(expected),'source History inputs missing')
    require(set(observed['audit'])==fingerprints,'audit query coverage differs')
    triggers=[]; linked=[]; audit_ids=set()
    for fp,value in observed['audit'].items():
        rows=page(value,'audit')
        for row in rows:
            require(existing.positive(row.get('id')) and row['id'] not in audit_ids and row.get('alert_fingerprint')==fp,'audit source identity differs')
            audit_ids.add(row['id'])
            require(row.get('rule_id')==rule['id'] and row.get('rule_name')==rule['name'] and row.get('pattern')==case['pattern'] and start<=instant(row.get('created_at'))<=end,'audit rule/time differs')
            require(row.get('outcome') in ['enriched','parent_emitted','released_no_trigger'],'unexpected dispatch/suppression decision')
            if row['outcome']=='parent_emitted': triggers.append(row)
            if case['pattern']=='parent_child':
                source=next(source for source,native in active.items() if native==fp)
                if expected[source]['labels']['alertname'].endswith('_parent'):
                    require(row.get('outcome')=='enriched' and row.get('reason')=='parent_registered' and row.get('parent_fingerprint')==fp,'existing parent registration missing')
                elif row.get('parent_fingerprint'): linked.append(row)
        # The unmatched Persistence negative intentionally has no selected rule.
        if not(case['pattern']=='persistence' and case['polarity']=='negative'): require(rows,'source decision missing')
    require(len(parent)==case['expected_parent_history'] and len(triggers)==case['expected_parent_history'],'physical parent/trigger count differs')
    if parent:
        row,labels=parent[0]; trigger=triggers[0]; window=trigger.get('window_id'); fp=trigger.get('parent_fingerprint')
        require(type(window) is str and 0<len(window)<=64 and type(fp) is str and 0<len(fp)<=128,'audit parent binding missing')
        want={'alertname':parent_name,'qualification_pack':build.PROFILE,'qualification_run':run,'site':case['site'], 'oscar_synthetic_source':'correlator','oscar_correlation_rule_id':str(rule['id']),'oscar_correlation_rule_name':rule['name'],'oscar_correlation_window_id':window,'correlation_parent_identity':parent_identity(rule['id'],window),'am_fingerprint':fp,'oscar_fingerprint':row.get('fingerprint')}
        require(all(labels.get(k)==v for k,v in want.items()) and row.get('fingerprint')==fp and row['fingerprint'] not in {item['fingerprint'] for item in history.values()} and fp not in fingerprints,'physical parent attribution differs')
        if case['pattern']=='persistence':
            due=instant(inputs[0]['startsAt'])+timedelta(seconds=30)
            require(instant(trigger.get('created_at'))>=due and instant(row.get('createdAt'))>=due and instant(row.get('startsAt'))>=due,'Persistence parent before unresolved deadline')
        require(row.get('status')=='firing' and start<=instant(row.get('startsAt'))<=end and (row.get('severity')=='critical' or labels.get('severity')=='critical') and row.get('severity') in (None,'critical') and labels.get('severity') in (None,'critical'),'physical parent severity/state differs')
    require(len(linked)==case['expected_existing_parent_links'],'existing parent link count differs')
    if linked:
        source=next(source for source,body in expected.items() if body['labels']['alertname'].endswith('_parent'))
        child=next(source for source,body in expected.items() if body['labels']['alertname'].endswith('_child'))
        require(linked[0]['parent_fingerprint']==active[source] and linked[0]['alert_fingerprint']==active[child],'existing parent link attribution differs')
    return {'sources':len(history),'synthetic_parents':len(parent),'existing_parent_links':len(linked),'audit_rows':len(audit_ids)}


def run(directory,pin,target,receiver,api_token,receiver_token,ownership,evidence,case_id,*,ca_file=None,transport=None,now=None,monotonic=None,sleep=None):
    now=now or (lambda:datetime.now(timezone.utc)); monotonic=monotonic or time.monotonic; sleep=sleep or time.sleep
    target=existing.origin(target); existing.ownership_check(ownership,target)
    parsed=urlsplit(receiver); base=urlsplit(target)
    require(parsed.scheme=='https' and parsed.netloc==base.netloc and parsed.path=='/webhookreceiver' and not parsed.query and not parsed.fragment,'same-origin verified receiver required')
    require(all(type(token) is str and len(token)>=16 and not any(c.isspace() for c in token) for token in [api_token,receiver_token]),'credential environment required')
    manifest=build.verify(directory); require(common.digest(common.read(Path(directory)/'manifest.json'))==pin,'manifest pin mismatch')
    case=next((case for case in build.scenario()['cases'] if case['id']==case_id),None); require(case is not None,'fixed case required')
    journal=Journal(evidence,[api_token,receiver_token]); evidence=Path(evidence)
    original=evidence/'kit'; original.mkdir(mode=0o700)
    for name in build.FILES+('manifest.json','manifest.sha256'): existing._write(original/name,common.read(Path(directory)/name))
    require(build.verify(original)==manifest,'kit snapshot differs')
    api=Endpoint(target,api_token,journal,ca_file=ca_file,transport=transport,now=now,monotonic=monotonic)
    sender=Endpoint(receiver,receiver_token,journal,ca_file=ca_file,transport=transport,now=now,monotonic=monotonic)
    report={'format':'corrtest-showcase-run-v2','manifest_sha256':pin,'case':case_id,'qualification_run':uuid4().hex,'target':target,'receiver':receiver,'ownership':ownership,'verdict':'INCONCLUSIVE','cleanup':'NOT_REQUIRED','limits':LIMITS,'inputs':[],'error':None}
    started=now(); initial_mono=monotonic(); report['started_at']=stamp(started)
    try:
        before=preflight(api,original); journal.save('preflight.json',before)
        rule=next(row for row in before['rules']['rows'] if row.get('name')=='corrtest-showcase-v2-'+case['pattern'])
        start=now().replace(microsecond=0); report['input_start']=stamp(start)
        for index,item in enumerate(case['inputs']):
            if index: sleep(3)
            require(monotonic()-initial_mono<45 and now()>=started and now()-started<timedelta(seconds=45),'finite input deadline exceeded')
            labels={'alertname':item['alertname'],'qualification_pack':build.PROFILE,'qualification_run':report['qualification_run'],'site':case['site'],'source_id':report['qualification_run']+'-'+item['source_id'],'severity':'warning','device':item['device'],'oscar_source':item['oscar_source']}
            body={'labels':labels,'startsAt':stamp(start+timedelta(seconds=item['at_seconds'])),'endsAt':stamp(start+timedelta(seconds=600))}
            report['inputs'].append(body); report['cleanup']='UNKNOWN'
            sender.exchange('POST',POST,body,manifest['profile']['effective_sha256'])
            report['cleanup']='DIRTY'
        sent=instant(sender.last_response_timing['observed_at']); sent_mono=sender.last_response_timing['monotonic_ns']/1_000_000_000; report['last_forward_at']=stamp(sent)
        for _ in range(9):
            sleep(10)
            require(now()>=sent and monotonic()>=sent_mono,'clock moved backwards')
        require(monotonic()-sent_mono>=90 and now()-sent>=timedelta(seconds=90) and now()-started<=timedelta(seconds=180),'full bounded observation interval required')
        end=now(); report['observation_end']=stamp(end); report['observation_elapsed_seconds']=int(monotonic()-sent_mono)
        observation=observe(api,case,report['qualification_run'],start,end)
        report['observation_elapsed_seconds']=(observation['capture']['monotonic_ns']-sender.last_response_timing['monotonic_ns'])//1_000_000_000
        journal.save('observation.json',observation)
        after=preflight(api,original); journal.save('postflight.json',after); require(before==after,'configuration/runtime changed')
        report['evidence']=evaluate(case,report['qualification_run'],start,end,report['inputs'],observation,rule)
        report['verdict']='PASS'
    except (Exception,KeyboardInterrupt) as error:
        report['error']=journal.redact(type(error).__name__+': '+str(error))[:2048]
    finally:
        api.client.close(); sender.client.close()
    report['finished_at']=stamp(now()); journal.save('report.json',report)
    inventory={name:common.digest(common.read(evidence/name)) for name in sorted(common.inventory(evidence))}
    existing._write(evidence/'evidence-manifest.json',common.canonical({'format':'corrtest-showcase-evidence-v2','files':inventory}))
    verify(evidence,pin)
    return report


class Replay:
    """Replay the original finite HTTP transcript without credentials or I/O."""
    def __init__(self, root, names, report):
        self.root, self.report, self.position = root, report, 0
        self.names = sorted(name for name in names if re.fullmatch(r'\d{4}-before.json', name))
        require(self.names == [f'{index:04d}-before.json' for index in range(1,len(self.names)+1)], 'journal operation gap')
    def exchange(self, method, path, body=None, revision=None):
        require(self.position<len(self.names),'original HTTP observation missing')
        name=self.names[self.position]; self.position+=1
        record=strict(common.read(self.root/name))
        raw=common.canonical(body) if body is not None else None
        expected={'method':method,'base':self.report['receiver'] if method=='POST' else self.report['target'],'path':path,
                  'body_base64':base64.b64encode(raw).decode() if raw is not None else None,
                  'body_sha256':common.digest(raw) if raw is not None else None,'profile_revision':revision}
        self.last_request_timing={key:record['request'].get(key) for key in ['observed_at','monotonic_ns']}
        expected.update(self.last_request_timing)
        require(record=={'operation':'showcase-http','responsibility':'UNKNOWN-until-recorded-response','request':expected},'original HTTP request differs')
        response=strict(common.read(self.root/name.replace('-before.json','-after.json')))
        raw=base64.b64decode(response['body_base64'],validate=True)
        require(response['status']==200 and common.digest(raw)==response['body_sha256'],'original HTTP success missing')
        result=strict(raw)
        if method=='POST': require(result=={'status':'ok'} and response['profile_revisions']==[revision],'original forwarding acknowledgment missing')
        return result


def acknowledged(response,revision):
    try:
        return response['status']==200 and strict(base64.b64decode(response['body_base64'],validate=True))=={'status':'ok'} and response['profile_revisions']==[revision]
    except (ValueError,TypeError,KeyError):
        return False


def verify(directory,pin):
    """No target access. Verify bytes, send journal and fresh semantic evaluation."""
    root=Path(directory); manifest=strict(common.read(root/'evidence-manifest.json'))
    require(set(manifest)=={'format','files'} and manifest['format']=='corrtest-showcase-evidence-v2','evidence manifest invalid')
    require(common.inventory(root)==set(manifest['files'])|{'evidence-manifest.json'},'evidence inventory differs')
    for name,digest in manifest['files'].items(): require(common.digest(common.read(root/name))==digest,'evidence bytes changed')
    report=strict(common.read(root/'report.json')); require(report.get('format')=='corrtest-showcase-run-v2' and report.get('manifest_sha256')==pin and report.get('limits')==LIMITS,'report binding differs')
    existing.ownership_check(report.get('ownership'),existing.origin(report.get('target')))
    endpoint=urlsplit(report.get('receiver')); origin=urlsplit(report['target'])
    require(endpoint.scheme=='https' and endpoint.netloc==origin.netloc and endpoint.path=='/webhookreceiver' and not endpoint.query and not endpoint.fragment,'export receiver origin differs')
    kit=build.verify(root/'kit'); require(common.digest(common.read(root/'kit/manifest.json'))==pin,'original manifest pin differs')
    case=next((c for c in build.scenario()['cases'] if c['id']==report.get('case')),None); require(case is not None,'case binding missing')
    attempts=[]; outcomes=[]; previous_time=None; previous_mono=-1; final_send=None; first_observation=None
    for name in sorted(manifest['files']):
        if re.fullmatch(r'\d{4}-before.json',name):
            before=strict(common.read(root/name)); request=before['request']
            request_time=instant(request.get('observed_at')); request_mono=request.get('monotonic_ns')
            require(type(request_mono) is int and request_mono>=0,'journal request clock invalid')
            if report['verdict']=='PASS': require(request_mono>=previous_mono and (previous_time is None or request_time>=previous_time),'journal request clock regression')
            previous_time,previous_mono=request_time,request_mono
            if final_send is not None and first_observation is None and request['method']=='GET': first_observation=request
            after=root/(name.replace('-before.json','-after.json'))
            if after.exists():
                response=strict(common.read(after)); raw=base64.b64decode(response['body_base64'],validate=True)
                require(common.digest(raw)==response['body_sha256'],'raw response hash differs')
                response_time=instant(response.get('observed_at')); response_mono=response.get('monotonic_ns')
                require(type(response_mono) is int and response_mono>=0,'journal response clock invalid')
                if report['verdict']=='PASS': require(response_mono>=previous_mono and response_time>=previous_time,'journal response clock regression')
                previous_time,previous_mono=response_time,response_mono
                if request['method']=='POST': final_send=response
            if request['method']=='POST':
                require(request['base']==report['receiver'] and request['path']==POST and request['profile_revision']==kit['profile']['effective_sha256'],'send endpoint/revision differs')
                raw=base64.b64decode(request['body_base64'],validate=True); require(common.digest(raw)==request['body_sha256'],'send bytes differ')
                attempts.append(strict(raw)); outcomes.append(after.exists() and acknowledged(response,kit['profile']['effective_sha256']))
            else: require(request['method']=='GET','forbidden remote mutation')
    require(attempts==report['inputs'] and len(attempts)<=len(case['inputs']),'attempt journal differs')
    cleanup='UNKNOWN' if not all(outcomes) else 'DIRTY' if attempts else 'NOT_REQUIRED'
    require(report['cleanup']==cleanup,'retained responsibility differs')
    if report['verdict']=='PASS':
        require(len(attempts)==len(case['inputs']) and all(outcomes) and report.get('error') is None,'PASS lacks confirmed inputs')
        replay=Replay(root,manifest['files'],report)
        before=preflight(replay,root/'kit')
        require(before==strict(common.read(root/'preflight.json')),'preflight differs from original HTTP')
        for body in attempts: replay.exchange('POST',POST,body,kit['profile']['effective_sha256'])
        start=instant(report['input_start']); end=instant(report['observation_end']); last=instant(report['last_forward_at'])
        require(final_send is not None and first_observation is not None and last==instant(final_send['observed_at']) and end<=instant(first_observation['observed_at'])<=end+timedelta(seconds=1) and report['observation_elapsed_seconds']==(first_observation['monotonic_ns']-final_send['monotonic_ns'])//1_000_000_000,'report timing differs from original journal')
        require(end-last>=timedelta(seconds=90) and end-start<=timedelta(seconds=180) and report['observation_elapsed_seconds']>=90,'PASS observation interval missing')
        for item,body in zip(case['inputs'],attempts,strict=True):
            expected={'alertname':item['alertname'],'qualification_pack':build.PROFILE,'qualification_run':report['qualification_run'],'site':case['site'],'source_id':report['qualification_run']+'-'+item['source_id'],'severity':'warning','device':item['device'],'oscar_source':item['oscar_source']}
            require(body=={'labels':expected,'startsAt':stamp(start+timedelta(seconds=item['at_seconds'])),'endsAt':stamp(start+timedelta(seconds=600))},'fixed input contract differs')
        observation=observe(replay,case,report['qualification_run'],start,end)
        require(observation==strict(common.read(root/'observation.json')),'observation differs from original HTTP')
        after=preflight(replay,root/'kit')
        require(before==after and after==strict(common.read(root/'postflight.json')),'PASS configuration changed')
        require(replay.position==len(replay.names),'unused original HTTP operations')
        rule=next(row for row in before['rules']['rows'] if row.get('name')=='corrtest-showcase-v2-'+case['pattern'])
        result=evaluate(case,report['qualification_run'],start,end,attempts,observation,rule)
        require(result==report['evidence'],'semantic result differs')
    else: require(report['verdict']=='INCONCLUSIVE' and report.get('error'),'invalid failure report')
    return report


def main():
    parser=argparse.ArgumentParser(description=__doc__); sub=parser.add_subparsers(dest='command',required=True)
    execute=sub.add_parser('run'); execute.add_argument('--kit',required=True,type=Path); execute.add_argument('--manifest-sha256',required=True)
    execute.add_argument('--api-base',required=True); execute.add_argument('--receiver-base',required=True); execute.add_argument('--ownership',required=True,type=Path)
    execute.add_argument('--case',required=True,choices=[case['id'] for case in build.scenario()['cases']]); execute.add_argument('--evidence',required=True,type=Path)
    execute.add_argument('--api-key-env',default='CORRTEST_API_KEY'); execute.add_argument('--receiver-key-env',default='CORRTEST_RECEIVER_KEY'); execute.add_argument('--ca-file')
    check=sub.add_parser('verify'); check.add_argument('--evidence',required=True,type=Path); check.add_argument('--manifest-sha256',required=True)
    args=parser.parse_args()
    if args.command=='verify': report=verify(args.evidence,args.manifest_sha256)
    else: report=run(args.kit,args.manifest_sha256,args.api_base,args.receiver_base,os.environ.get(args.api_key_env,''),os.environ.get(args.receiver_key_env,''),strict(common.read(args.ownership)),args.evidence,args.case,ca_file=args.ca_file)
    print(json.dumps({key:report[key] for key in ['case','verdict','cleanup','error']},sort_keys=True))
    return 4 if report['cleanup'] in ('DIRTY','UNKNOWN') else 3 if report['verdict']!='PASS' else 0

if __name__=='__main__': raise SystemExit(main())
