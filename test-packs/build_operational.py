#!/usr/bin/env python3
"""Build one finite offline kit using the matching backend's real validators.

No target access, installer, generated rule substitution, or execution authority.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import zipfile

ID = 'corrtest_operational'
VERSION = '0.1.0'
PROFILE = 'corrtest_operational_v1'
RULE = 'corrtest-operational-v1-flood'
ARCHIVE = f'{ID}-{VERSION}.tar.gz'
MAX_BYTES = 1_048_576
SOURCE_FILES = frozenset({'project/pack.source.yml', f'project/conf/mappings/{PROFILE}.json', 'correlation/flood.yml', 'scenarios/flood-installed.json', 'README.md'})
ARTIFACTS = sorted([ARCHIVE, ARCHIVE + '.sha256', 'correlation-rules.zip', 'flood-installed.json', 'hash-vectors.json', 'README.md'])
DIGEST = re.compile(r'[0-9a-f]{64}\Z')

# Imported backend Python sources are recorded, including transitive imports.
PROVENANCE = '''
root = Path(sys.argv[1]).resolve()
sources = {}
for module in tuple(sys.modules.values()):
    path = getattr(module, "__file__", None)
    if not path:
        continue
    path = Path(path).resolve()
    if path.suffix == ".py" and path.is_relative_to(root):
        sources[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
result["sources"] = sources
print(json.dumps(result, sort_keys=True, separators=(",", ":")))
'''
PROFILE_CODE = '''
import asyncio, hashlib, json, sys
from datetime import datetime, timezone
from pathlib import Path
from app.core.profiles.compiler import compile_profile
from app.core.profiles.executor import execute_profile
from app.core.profiles.models import RequestContext
value = json.load(sys.stdin)
row = value["profile"]
compiled = compile_profile(row["name"], row["description"], row["element"])
request = RequestContext(value["body"], {}, None, datetime(2026, 1, 1, tzinfo=timezone.utc))
alert = asyncio.run(execute_profile(compiled, request, None))
result = {"effective_sha256": compiled.effective_hash, "labels": dict(alert.labels), "annotations": dict(alert.annotations), "event_timestamp": alert.event_timestamp.isoformat(), "ends_at": alert.ends_at.isoformat()}
''' + PROVENANCE
RULE_CODE = '''
import hashlib, json, sys
from pathlib import Path
from types import SimpleNamespace
from project_pack.model import _load_strict_yaml
from app.core.rule_validation import validate_effective_rule, runtime_capabilities
from app.core.rule_portability import dump_rule_yaml, rule_to_authored_dict
from app.core.settings import Settings
from app.core.window_matching import clause_matches
rule = _load_strict_yaml(sys.stdin.read())
normalized = validate_effective_rule(rule, Settings(_env_file=None), runtime_capabilities(), record_rejections=False)
authored = rule_to_authored_dict(SimpleNamespace(**normalized))
if set(rule) != set(authored):
    raise ValueError("only complete authored rule fields are permitted")
if not clause_matches({"alertname": "CorrTestOperationalFlood"}, authored["match_criteria"]["match"]):
    raise ValueError("rule does not match the scenario alertname")
result = {"authored": authored, "yaml": dump_rule_yaml(SimpleNamespace(**authored))}
''' + PROVENANCE
PACK_CODE = '''
import hashlib, json, sys
from pathlib import Path
from packaging.version import Version
from project_pack.archive import write_pack
from project_pack.static_validation import ValidationContext
context = ValidationContext(Version("26.3.0"), frozenset(), frozenset(), frozenset(), frozenset())
pack = write_pack(Path(sys.argv[2]), Path(sys.argv[3]), context)
manifest = pack.inspection.manifest
if (manifest.schema_version != 1 or manifest.id != "corrtest_operational" or manifest.version != "0.1.0"
        or manifest.requires_oscar != ">=26.3.0,<27.0.0" or manifest.requires_tasks_common or manifest.requires_tasks_local):
    raise ValueError("unsupported project metadata")
result = {"sha256": pack.sha256, "files_checked": pack.validation_report.files_checked}
''' + PROVENANCE


def canonical(value):
    """UTF-8, sorted keys, no floats, no HTML escapes, LF; see shipped vectors."""
    def check(item):
        if item is None or type(item) in (bool, str):
            return
        if type(item) is int and -(2**53 - 1) <= item <= 2**53 - 1:
            return
        if type(item) is list:
            for child in item:
                check(child)
            return
        if type(item) is dict and all(type(key) is str for key in item):
            for child in item.values():
                check(child)
            return
        raise ValueError('unsupported canonical value')
    check(value)
    return (json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False) + '\n').encode('utf-8')


def digest(data):
    return hashlib.sha256(data).hexdigest()


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError('duplicate JSON key')
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=pairs)


def read(path):
    # O_NONBLOCK avoids waiting if an input is replaced with a FIFO; NOFOLLOW
    # binds the opened leaf, while fstat and read bound the actual descriptor.
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except OSError as error:
        raise ValueError('expected bounded regular file') from error
    with os.fdopen(fd, 'rb') as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError('expected bounded regular file')
        data = stream.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES:
        raise ValueError('expected bounded regular file')
    return data


def inventory(root, *, flat=False):
    if root.is_symlink() or not root.is_dir():
        raise ValueError('expected real directory')
    found = set()
    for path in root.rglob('*'):
        mode = path.lstat().st_mode
        if stat.S_ISDIR(mode):
            if flat:
                raise ValueError('unexpected artifact directory')
            continue
        read(path)
        found.add(path.relative_to(root).as_posix())
    return found


def backend_call(backend, roots, code, data, *args, label):
    env = {'PATH': os.defpath, 'PYTHONDONTWRITEBYTECODE': '1', 'USE_VAULT': 'false',
           'PYTHONPATH': os.pathsep.join(str(backend / path) for path in roots)}
    result = subprocess.run([sys.executable, '-c', code, str(backend), *map(str, args)], input=data,
                            env=env, cwd=backend, capture_output=True, timeout=60)
    if result.returncode:
        raise ValueError(label + ' validation failed:\n' + result.stderr.decode('utf-8', errors='replace'))
    return strict_json(result.stdout)


def sample_body():
    return {'labels': {'alertname': 'CorrTestOperationalFlood', 'qualification_pack': PROFILE,
                       'qualification_run': 'offline-vector', 'site': 'positive', 'source_id': 'source-1', 'severity': 'warning'},
            'startsAt': '2026-01-01T00:00:00Z', 'endsAt': '2026-01-01T00:10:00Z'}


def validate_profile(backend, path, body):
    rows = strict_json(read(path))
    if type(rows) is not list or len(rows) != 1:
        raise ValueError('exactly one profile required')
    return backend_call(backend, ['oscar-webhookreceiver/src'], PROFILE_CODE,
                        canonical({'profile': rows[0], 'body': body}), label='profile')


def expected_scenario():
    return {'apiVersion': 'corrtest.oscar/flood-scenario-v1', 'id': 'flood-installed-v1',
            'coverage': 'ordinary-initial-flood-audit', 'profile': PROFILE, 'rule': RULE,
            'labels': {'alertname': 'CorrTestOperationalFlood', 'qualification_pack': PROFILE, 'severity': 'warning'},
            'identity': {'run': 'fresh-unpredictable-per-run', 'source_id': 'unique-per-input', 'start': 'one-captured-initial-start-per-run'},
            'groups': [{'site': site, 'count': n, 'expected_enriched': enriched, 'expected_parent_emitted': parents}
                       for site, n, enriched, parents in [('positive', 5, 4, 1), ('negative', 4, 4, 0), ('isolated-a', 3, 3, 0), ('isolated-b', 3, 3, 0)]],
            'budgets': {'validity_seconds': 600, 'execution_seconds': 180, 'window_seconds': 60, 'observation_lag_seconds': 30, 'poll_seconds': 1},
            'cleanup': 'DIRTY-until-owned-target-teardown',
            'unsupported': ['dispatch', 'clear', 'refire', 'restart', 'faults', 'enhanced-admission', 'other-patterns']}


def metadata():
    return {'apiVersion': 'corrtest.oscar/test-pack-v1', 'id': ID, 'version': VERSION,
            'requires_oscar': '>=26.3.0,<27.0.0', 'coverage': 'ordinary-initial-flood-audit',
            'scenario': 'flood-installed-v1', 'validation': 'offline-only-reference-settings-26.3.0',
            'requires': ['exclusive-owned-target', 'no-competing-writers-or-rules-through-export', 'stable-configuration-and-processes-through-export',
                         'controlled-lab-destinations-history-only', 'authenticated-target-access',
                         'receiver-profile-reload-readback', 'correlation-rule-load-readback', 'active-history-audit-readiness',
                         'authoritative-false-and-dispatch-enabled-false', 'target-owned-teardown'],
            'unsupported': expected_scenario()['unsupported']}


def build(source, backend, output):
    source, backend, output = Path(source), Path(backend).resolve(), Path(output)
    if output.exists() or output.is_symlink():
        raise ValueError('output must not already exist')
    if inventory(source) != SOURCE_FILES:
        raise ValueError('unexpected source inventory')
    # Exactly five bounded inputs. Validators and pack writer only receive this
    # private copy, so subsequent authored-file edits cannot split their views.
    with tempfile.TemporaryDirectory(prefix='corrtest-operational-input-') as temporary:
        snapshot = Path(temporary)
        for name in sorted(SOURCE_FILES):
            destination = snapshot / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(read(source / name))
        return _build_snapshot(snapshot, backend, output)


def _build_snapshot(source, backend, output):
    source, backend, output = Path(source), Path(backend).resolve(), Path(output)
    if output.exists() or output.is_symlink():
        raise ValueError('output must not already exist')
    if inventory(source) != SOURCE_FILES:
        raise ValueError('unexpected source inventory')
    scenario = strict_json(read(source / 'scenarios/flood-installed.json'))
    if canonical(scenario) != canonical(expected_scenario()):
        raise ValueError('unsupported scenario')
    # Real effective-rule validation deliberately precedes the finite contract.
    rule = backend_call(backend, ['oscar-correlator/src', 'oscar-correlator/src/app', 'oscar-util/scripts'], RULE_CODE,
                        read(source / 'correlation/flood.yml'), label='rule')
    authored = rule['authored']
    if (authored['name'] != RULE or authored['pattern'] != 'flood' or authored['window_seconds'] != 60
            or authored['group_by_labels'] != ['qualification_run', 'site']
            or authored['match_criteria'] != {'match': {'alertname': 'CorrTestOperationalFlood'}, 'min_count': 5}
            or authored['correlation_policy'] is not None or authored['enabled'] is not True
            or authored['emit_spec'] != {'alertname': 'CorrTestOperationalFloodParent'}
            or authored['for_notifiers'] is not None or authored['max_synthetic_per_minute'] != 10):
        raise ValueError('unsupported authored rule')
    profile_path = source / f'project/conf/mappings/{PROFILE}.json'
    profile = validate_profile(backend, profile_path, sample_body())
    row = strict_json(read(profile_path))[0]
    labels = list(sample_body()['labels'])
    expected_elements = {'profile~schema_version': '1', 'profile~enabled': 'true',
                         'profile~required': ','.join('labels.' + key for key in labels) + ',event_timestamp,ends_at',
                         'parse_time~event_timestamp': 'body:/startsAt|rfc3339', 'parse_time~ends_at': 'body:/endsAt|rfc3339'}
    for index, key in enumerate(labels):
        expected_elements['select~work.' + key] = 'body:/labels/' + key
        expected_elements['when~' + key] = json.dumps({'order': index, 'condition': 'work.' + key + r' regex \S',
                                                     'action': {'command': 'select', 'target': 'labels.' + key,
                                                                'value': 'field:work.' + key}}, separators=(',', ':'))
    if {item['key']: item['value'] for item in row['element']} != expected_elements:
        raise ValueError('unsupported profile operations/requirements')
    if (row['name'] != PROFILE or row['mapping_namespace_name'] != 'webhook_profiles'
            or row['description'] != row['description'].lower()
            or profile['labels'] != sample_body()['labels'] or profile['annotations'] != {}):
        raise ValueError('unsupported profile identity/output')
    output.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix='.operational-', dir=output.parent))
    try:
        pack = backend_call(backend, ['oscar-util/scripts'], PACK_CODE, b'', source / 'project', staging, label='project-pack')
        if pack['files_checked'] != 1:
            raise ValueError('unexpected project pack inventory')
        with zipfile.ZipFile(staging / 'correlation-rules.zip', 'w', compression=zipfile.ZIP_STORED) as archive:
            item = zipfile.ZipInfo(RULE + '.yml', (1980, 1, 1, 0, 0, 0))
            item.create_system, item.external_attr = 3, 0o100644 << 16
            archive.writestr(item, rule['yaml'].encode('utf-8'))
        (staging / 'flood-installed.json').write_bytes(canonical(scenario))
        (staging / 'README.md').write_bytes(read(source / 'README.md'))
        vector_values = [{}, {'z': 0, 'a': ['<>&', 'é', '\n', True, None]}, authored, row]
        vectors = [{'value': value, 'utf8_hex': canonical(value).hex(), 'sha256': digest(canonical(value))} for value in vector_values]
        (staging / 'hash-vectors.json').write_bytes(canonical({'format': 'corrtest-canonical-json-v1', 'vectors': vectors}))
        manifest = {**metadata(), 'profile': {'name': PROFILE, 'namespace': 'webhook_profiles', 'nested_mappings': [],
                                             'effective_sha256': profile['effective_sha256'], 'mapping_sha256': digest(canonical(row))},
                    'rule': {'name': RULE, 'authored_sha256': digest(canonical(authored))},
                    'validator_sources': {**pack['sources'], **profile['sources'], **rule['sources']},
                    'files': [{'path': name, 'size': len(read(staging / name)), 'sha256': digest(read(staging / name))} for name in ARTIFACTS]}
        raw = canonical(manifest)
        (staging / 'manifest.json').write_bytes(raw)
        (staging / 'manifest.sha256').write_text(digest(raw) + '\n', encoding='ascii')
        verify(staging)
        staging.rename(output)
    finally:
        if staging.exists():
            shutil.rmtree(staging)
    return manifest


def verify(directory):
    """Closed v1 transport contract. Hashes bind bytes; they grant no authority."""
    directory = Path(directory)
    if inventory(directory, flat=True) != set(ARTIFACTS) | {'manifest.json', 'manifest.sha256'}:
        raise ValueError('unexpected artifact inventory')
    raw = read(directory / 'manifest.json')
    manifest = strict_json(raw)
    if canonical(manifest) != raw or read(directory / 'manifest.sha256') != (digest(raw) + '\n').encode('ascii'):
        raise ValueError('manifest canonical/hash mismatch')
    if type(manifest) is not dict or set(manifest) != set(metadata()) | {'profile', 'rule', 'validator_sources', 'files'}:
        raise ValueError('unexpected manifest fields')
    if any(manifest[key] != value for key, value in metadata().items()):
        raise ValueError('unsupported manifest metadata')
    profile, rule = manifest['profile'], manifest['rule']
    if (type(profile) is not dict or set(profile) != {'name', 'namespace', 'nested_mappings', 'effective_sha256', 'mapping_sha256'}
            or profile['name'] != PROFILE or profile['namespace'] != 'webhook_profiles' or profile['nested_mappings'] != []
            or type(rule) is not dict or set(rule) != {'name', 'authored_sha256'} or rule['name'] != RULE):
        raise ValueError('invalid profile/rule manifest')
    sources = manifest['validator_sources']
    if (type(sources) is not dict or not sources or len(sources) > 1000
            or any(not re.fullmatch(r'(oscar-util/scripts/project_pack|oscar-correlator/src/app|oscar-webhookreceiver/src/app)/[A-Za-z0-9_./-]+\.py', key)
                   or '..' in key.split('/') for key in sources)):
        raise ValueError('invalid validator sources')
    hashes = [profile['effective_sha256'], profile['mapping_sha256'], rule['authored_sha256'], *sources.values()]
    if any(type(value) is not str or not DIGEST.fullmatch(value) for value in hashes):
        raise ValueError('invalid hash')
    files = manifest['files']
    if type(files) is not list or len(files) != len(ARTIFACTS):
        raise ValueError('invalid artifact count')
    for name, record in zip(ARTIFACTS, files, strict=True):
        if type(record) is not dict or set(record) != {'path', 'size', 'sha256'} or record['path'] != name:
            raise ValueError('invalid artifact path/fields')
        data = read(directory / name)
        if type(record['size']) is not int or not 0 < record['size'] <= MAX_BYTES or record['size'] != len(data) or record['sha256'] != digest(data):
            raise ValueError('artifact size/hash mismatch')
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backend', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    build(Path(__file__).parent / 'correlation-operational-v1', args.backend, args.output)
    print(str(args.output / 'manifest.json'))


if __name__ == '__main__':
    main()
