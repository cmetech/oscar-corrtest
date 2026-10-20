#!/usr/bin/env python3
"""Build disabled ordinary-pattern showcase artifacts with real backend validators.

No installation, source capability, HTTP execution, or UAT acceptance is granted.
"""
import argparse
import json
from pathlib import Path
import shutil
import tempfile
import zipfile

import build_operational as common


PROFILE = 'corrtest_showcase_v2'
PACK_ID = 'corrtest_showcase'
VERSION = '0.2.0'
PATTERNS = ('flood', 'co_occurrence', 'sequence', 'threshold', 'cross_source', 'persistence', 'parent_child')
REQUIRES = ['exclusive-owned-target', 'retained-v1-inventory-readback', 'exact-seven-rule-runtime-readback',
            'history-only-controlled-destinations', 'coordinated-ordinary-authoritative-dispatch-readback', 'enhanced-production-disabled-readback',
            'profile-revision-precondition-and-readback', 'durable-input-before-send-no-automatic-retry',
            'complete-native-active-source-history-audit', 'physical-parent-history-not-audit-only',
            'monotonic-and-wall-clock-observation', 'independent-export-verification']
FILES = ('correlation-rules.zip', 'corrtest_showcase-0.2.0.tar.gz', 'corrtest_showcase-0.2.0.tar.gz.sha256',
         'profile.json', 'rules.json', 'showcase.json')

RULE_CODE = '''
import hashlib, json, sys
from pathlib import Path
from types import SimpleNamespace
from app.core.rule_validation import validate_effective_rule, runtime_capabilities
from app.core.rule_portability import dump_rule_yaml, rule_to_authored_dict
from app.core.settings import Settings
items = []
for rule in json.load(sys.stdin):
    normalized = validate_effective_rule(rule, Settings(_env_file=None), runtime_capabilities(), record_rejections=False)
    items.append({"authored": rule_to_authored_dict(SimpleNamespace(**normalized)),
                  "yaml": dump_rule_yaml(SimpleNamespace(**normalized))})
result = {"rules": items}
''' + common.PROVENANCE

PACK_CODE = '''
import hashlib, json, sys
from pathlib import Path
from packaging.version import Version
from project_pack.archive import write_pack
from project_pack.static_validation import ValidationContext
context = ValidationContext(Version("26.4.3"), frozenset(), frozenset(), frozenset(), frozenset())
pack = write_pack(Path(sys.argv[2]), Path(sys.argv[3]), context)
if pack.inspection.manifest.id != "corrtest_showcase" or pack.validation_report.files_checked != 1:
    raise ValueError("unexpected showcase project inventory")
result = {"sha256": pack.sha256}
''' + common.PROVENANCE


def alertname(pattern, role='signal'):
    return 'CorrTestShowcaseV2_' + pattern + '_' + role


def rules():
    result = []
    for pattern in PATTERNS:
        match = {'alertname': alertname(pattern)}
        criteria = {'match': match}
        if pattern == 'flood':
            criteria['min_count'] = 5
        elif pattern == 'threshold':
            criteria.update(distinct_label='device', min_distinct_count=3)
        elif pattern == 'persistence':
            criteria['unresolved_for_seconds'] = 30
        elif pattern == 'co_occurrence':
            criteria = {'required_matches': [{'alertname': alertname(pattern, role)} for role in ('a', 'b')], 'min_matches': 2}
        elif pattern == 'sequence':
            criteria = {'sequence': [{'alertname': alertname(pattern, role)} for role in ('a', 'b')]}
        elif pattern == 'cross_source':
            criteria = {'required_sources': [{'source': value, 'match': match} for value in ('snmp', 'api')]}
        elif pattern == 'parent_child':
            criteria = {'parent_match': {'alertname': alertname(pattern, 'parent')},
                        'child_matches': [{'alertname': alertname(pattern, 'child')}],
                        'suppress_children_for_notifiers': [], 'tag_children_for_notifiers': []}
        result.append({'name': 'corrtest-showcase-v2-' + pattern, 'pattern': pattern,
                       'window_seconds': 60, 'group_by_labels': ['qualification_run', 'site'],
                       'match_criteria': criteria, 'correlation_policy': None,
                       'emit_spec': None if pattern == 'parent_child' else {
                           'alertname': alertname(pattern, 'synthetic_parent'), 'severity': 'critical',
                           'labels': {'qualification_pack': PROFILE, 'qualification_run': '{group.qualification_run}',
                                      'site': '{group.site}'}},
                       'for_notifiers': None, 'priority': 100, 'max_synthetic_per_minute': 10, 'enabled': False,
                       'description': 'Owned ordinary-pattern showcase; retained; no exact-source or recovery qualification.'})
    return result


def sample_body():
    return {'labels': {'alertname': alertname('flood'), 'qualification_pack': PROFILE,
                       'qualification_run': 'offline-vector', 'site': 'flood-positive',
                       'source_id': 'source-1', 'severity': 'warning', 'device': 'device-1', 'oscar_source': 'snmp'},
            'startsAt': '2026-01-01T00:00:00Z', 'endsAt': '2026-01-01T00:10:00Z'}


def profile():
    labels = list(sample_body()['labels'])
    elements = [{'key': 'profile~schema_version', 'value': '1'}, {'key': 'profile~enabled', 'value': 'true'},
                {'key': 'profile~required', 'value': ','.join('labels.' + key for key in labels) + ',event_timestamp,ends_at'}]
    for order, key in enumerate(labels):
        elements.extend([{'key': 'select~work.' + key, 'value': 'body:/labels/' + key},
                         {'key': 'when~' + key, 'value': json.dumps({'order': order, 'condition': 'work.' + key + r' regex \S',
                          'action': {'command': 'select', 'target': 'labels.' + key, 'value': 'field:work.' + key}}, separators=(',', ':'))}])
    elements.extend([{'key': 'parse_time~event_timestamp', 'value': 'body:/startsAt|rfc3339'},
                     {'key': 'parse_time~ends_at', 'value': 'body:/endsAt|rfc3339'}])
    return {'name': PROFILE, 'mapping_namespace_name': 'webhook_profiles',
            'description': 'finite ordinary showcase inputs; no source authority or automatic clear.', 'element': elements}


def validate_profile(backend, body):
    return common.backend_call(Path(backend), ['oscar-webhookreceiver/src'], common.PROFILE_CODE,
                               common.canonical({'profile': profile(), 'body': body}), label='profile')


def scenario():
    cases = []
    for pattern in PATTERNS:
        for positive in (True, False):
            polarity = 'positive' if positive else 'negative'
            roles = ['signal']
            if pattern == 'flood':
                roles *= 5 if positive else 4
            elif pattern in ('co_occurrence', 'sequence'):
                roles = ['a', 'b'] if positive else (['a'] if pattern == 'co_occurrence' else ['b', 'a'])
            elif pattern == 'threshold':
                roles *= 3
            elif pattern == 'cross_source':
                roles *= 2
            elif pattern == 'parent_child':
                roles = ['parent', 'child'] if positive else ['child']
            elif pattern == 'persistence' and not positive:
                roles = ['unmatched']
            inputs = [{'at_seconds': i * 3, 'alertname': alertname(pattern, role), 'status': 'firing',
                       'source_id': f'{pattern}-{polarity}-{i + 1}', 'device': f'device-{i + 1}' if positive else 'device-one',
                       'oscar_source': 'api' if pattern == 'cross_source' and positive and i else 'snmp'}
                      for i, role in enumerate(roles)]
            cases.append({'id': pattern + '-' + polarity, 'pattern': pattern, 'polarity': polarity,
                          'site': pattern + '-' + polarity, 'inputs': inputs, 'observe_seconds': 90,
                          'expected_parent_history': 1 if positive and pattern != 'parent_child' else 0,
                          'expected_existing_parent_links': 1 if positive and pattern == 'parent_child' else 0})
    return {'apiVersion': 'corrtest.oscar/showcase-scenario-v2', 'profile': PROFILE,
            'coverage': 'ordinary-receiver-native-history-patterns', 'source_severity': 'warning',
            'parent_severity': 'critical', 'cases': cases, 'input_validity_seconds': 600,
            'cleanup': 'retained-dirty-no-automatic-clear-or-delete',
            'unsupported': ['exact-occurrence-persistence', 'production-duration-mapped-persistence',
                            'physical-recovery', 'collector-health-absence', 'notification-provider-delivery',
                            'crash-recovery', 'production-capacity']}


def metadata():
    return {'apiVersion': 'corrtest.oscar/showcase-kit-v2', 'id': PACK_ID, 'version': VERSION,
            'execution_authority': False, 'rules_authored_disabled': True,
            'retained_coexistence': ['corrtest-operational-v1-flood'], 'requires': REQUIRES,
            'coverage': scenario()['coverage']}


def build(backend, output):
    backend, output = Path(backend).resolve(), Path(output)
    if output.exists() or output.is_symlink():
        raise ValueError('output already exists')
    validated = common.backend_call(backend, ['oscar-correlator/src', 'oscar-correlator/src/app'],
                                    RULE_CODE, common.canonical(rules()), label='rules')
    authored = [item['authored'] for item in validated['rules']]
    if authored != rules():
        raise ValueError('backend normalized rule contract differs')
    profile_result = validate_profile(backend, sample_body())
    if profile_result['labels'] != sample_body()['labels'] or profile_result['annotations']:
        raise ValueError('profile output differs')
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix='.showcase-', dir=output.parent))
    try:
        with tempfile.TemporaryDirectory(prefix='corrtest-showcase-input-') as temporary:
            project = Path(temporary)
            (project / 'conf/mappings').mkdir(parents=True)
            (project / 'conf/mappings' / (PROFILE + '.json')).write_bytes(common.canonical([profile()]))
            (project / 'pack.source.yml').write_bytes(common.canonical({'schema_version': 1, 'id': PACK_ID,
                'version': VERSION, 'requires_oscar': '>=26.4.3,<27.0.0', 'requires_tasks': {'common': [], 'local': []}}))
            pack = common.backend_call(backend, ['oscar-util/scripts'], PACK_CODE, b'', project, staging, label='pack')
        with zipfile.ZipFile(staging / 'correlation-rules.zip', 'w', compression=zipfile.ZIP_STORED) as archive:
            for item in validated['rules']:
                member = zipfile.ZipInfo(item['authored']['name'] + '.yml', (1980, 1, 1, 0, 0, 0))
                member.create_system, member.external_attr = 3, 0o100644 << 16
                archive.writestr(member, item['yaml'].encode())
        for name, value in [('profile.json', profile()), ('rules.json', authored), ('showcase.json', scenario())]:
            (staging / name).write_bytes(common.canonical(value))
        manifest = {**metadata(), 'profile': {'name': PROFILE, 'effective_sha256': profile_result['effective_sha256']},
                    'validator_sources': {**validated['sources'], **profile_result['sources'], **pack['sources']},
                    'files': [{'path': name, 'size': len(common.read(staging / name)), 'sha256': common.digest(common.read(staging / name))} for name in FILES]}
        raw = common.canonical(manifest)
        (staging / 'manifest.json').write_bytes(raw)
        (staging / 'manifest.sha256').write_text(common.digest(raw) + '\n', encoding='ascii')
        verify(staging)
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    return manifest


def verify(directory):
    """Validate fixed metadata and byte inventory; this is not a trusted pin or deployment check."""
    directory = Path(directory)
    if common.inventory(directory, flat=True) != set(FILES) | {'manifest.json', 'manifest.sha256'}:
        raise ValueError('unexpected showcase inventory')
    raw = common.read(directory / 'manifest.json')
    manifest = common.strict_json(raw)
    if common.canonical(manifest) != raw or common.read(directory / 'manifest.sha256') != (common.digest(raw) + '\n').encode():
        raise ValueError('manifest encoding or hash differs')
    if set(manifest) != set(metadata()) | {'profile', 'validator_sources', 'files'} or any(manifest[key] != value for key, value in metadata().items()):
        raise ValueError('showcase metadata differs')
    if len(manifest['files']) != len(FILES):
        raise ValueError('artifact count differs')
    for name, record in zip(FILES, manifest['files'], strict=True):
        data = common.read(directory / name)
        if record != {'path': name, 'size': len(data), 'sha256': common.digest(data)}:
            raise ValueError('artifact binding differs')
    for name, expected in [('profile.json', profile()), ('rules.json', rules()), ('showcase.json', scenario())]:
        if common.read(directory / name) != common.canonical(expected):
            raise ValueError('fixed artifact contract differs')
    if manifest['profile']['name'] != PROFILE or not common.DIGEST.fullmatch(manifest['profile']['effective_sha256']):
        raise ValueError('invalid profile revision')
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backend', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    build(args.backend, args.output)
    print(args.output / 'manifest.json')


if __name__ == '__main__':
    main()
