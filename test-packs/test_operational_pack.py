"""Offline contract tests; every backend validator is real, never mocked."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import tarfile
import zipfile

import pytest
import yaml

HERE = Path(__file__).resolve().parent
BACKEND = HERE.parents[1] / 'oscar'


@pytest.fixture(scope='module')
def builder():
    path = HERE / 'build_operational.py'
    assert path.is_file(), 'offline operational pack builder is not implemented'
    spec = importlib.util.spec_from_file_location('operational_builder', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def source(tmp_path):
    target = tmp_path / 'source'
    shutil.copytree(HERE / 'correlation-operational-v1', target)
    return target


def test_real_build_deterministic_and_import_ready(builder, tmp_path):
    first, second = tmp_path / 'a', tmp_path / 'b'
    builder.build(HERE / 'correlation-operational-v1', BACKEND, first)
    builder.build(HERE / 'correlation-operational-v1', BACKEND, second)
    assert {p.name: p.read_bytes() for p in first.iterdir()} == {p.name: p.read_bytes() for p in second.iterdir()}
    manifest = builder.verify(first)
    assert manifest['coverage'] == 'ordinary-initial-flood-audit'
    assert manifest['profile']['nested_mappings'] == []
    assert manifest['profile']['effective_sha256'] == '725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb'
    with zipfile.ZipFile(first / 'correlation-rules.zip') as archive:
        assert archive.namelist() == ['corrtest-operational-v1-flood.yml']
        rule = yaml.safe_load(archive.read(archive.namelist()[0]))
    assert rule['match_criteria'] == {'match': {'alertname': 'CorrTestOperationalFlood'}, 'min_count': 5}
    assert rule['correlation_policy'] is None
    assert rule['group_by_labels'] == ['qualification_run', 'site']
    scenario = json.loads((first / 'flood-installed.json').read_bytes())
    assert [(g['site'], g['count']) for g in scenario['groups']] == [('positive', 5), ('negative', 4), ('isolated-a', 3), ('isolated-b', 3)]


@pytest.mark.parametrize('label', ['qualification_run', 'site', 'source_id'])
@pytest.mark.parametrize('value', [None, '', '   '])
def test_real_profile_refuses_missing_or_blank_group_identity(builder, label, value):
    body = builder.sample_body()
    if value is None:
        del body['labels'][label]
    else:
        body['labels'][label] = value
    with pytest.raises(ValueError, match='profile validation'):
        builder.validate_profile(BACKEND, HERE / 'correlation-operational-v1/project/conf/mappings/corrtest_operational_v1.json', body)


def test_real_profile_drops_unselected_control_labels(builder):
    body = builder.sample_body()
    body['labels']['oscar_test_run_id'] = 'not-authority'
    result = builder.validate_profile(BACKEND, HERE / 'correlation-operational-v1/project/conf/mappings/corrtest_operational_v1.json', body)
    assert result['labels'] == {key: value for key, value in body['labels'].items() if key != 'oscar_test_run_id'}
    assert result['event_timestamp'] == '2026-01-01T00:00:00+00:00'
    assert result['ends_at'] == '2026-01-01T00:10:00+00:00'


def test_real_rule_validator_rejects_proposed_extended_legacy_match(builder, source, tmp_path):
    path = source / 'correlation/flood.yml'
    rule = yaml.safe_load(path.read_text())
    rule['match_criteria']['match']['match_labels'] = {'qualification_pack': 'corrtest_operational_v1'}
    path.write_text(yaml.safe_dump(rule))
    with pytest.raises(ValueError, match='label/composite matching requires durable episode policy'):
        builder.build(source, BACKEND, tmp_path / 'dist')
    assert not (tmp_path / 'dist').exists()


def test_real_pack_validator_rejects_reserved_mapping(builder, source, tmp_path):
    path = source / 'project/conf/mappings/corrtest_operational_v1.json'
    rows = json.loads(path.read_bytes())
    rows[0]['name'] = 'splunk'
    path.write_text(json.dumps(rows))
    with pytest.raises(ValueError):
        builder.build(source, BACKEND, tmp_path / 'dist')
    assert not (tmp_path / 'dist').exists()


@pytest.mark.parametrize('kind', ['extra', 'symlink', 'scenario'])
def test_source_rejects_extra_links_or_changed_scenario(builder, source, tmp_path, kind):
    if kind == 'extra':
        (source / 'credentials.json').write_text('{}')
    elif kind == 'symlink':
        path = source / 'correlation/flood.yml'
        path.unlink()
        path.symlink_to(HERE / 'correlation-operational-v1/correlation/flood.yml')
    else:
        path = source / 'scenarios/flood-installed.json'
        value = json.loads(path.read_bytes())
        value['groups'][0]['count'] = 4
        path.write_text(json.dumps(value))
    with pytest.raises(ValueError):
        builder.build(source, BACKEND, tmp_path / 'dist')
    assert not (tmp_path / 'dist').exists()


@pytest.mark.parametrize('kind', ['bytes', 'extra', 'unknown_field', 'bool_size', 'traversal', 'duplicate_key'])
def test_manifest_rejects_corruption_and_ambiguous_input(builder, tmp_path, kind):
    output = tmp_path / 'out'
    builder.build(HERE / 'correlation-operational-v1', BACKEND, output)
    manifest = json.loads((output / 'manifest.json').read_bytes())
    if kind == 'bytes':
        (output / 'correlation-rules.zip').write_bytes(b'wrong')
    elif kind == 'extra':
        (output / 'other').write_bytes(b'wrong')
    else:
        if kind == 'unknown_field':
            manifest['authority'] = True
        elif kind == 'bool_size':
            manifest['files'][0]['size'] = True
        elif kind == 'traversal':
            manifest['files'][0]['path'] = '../outside'
        raw = builder.canonical(manifest)
        if kind == 'duplicate_key':
            raw = raw.replace(b'{', b'{"version":"0.1.0",', 1)
        (output / 'manifest.json').write_bytes(raw)
        (output / 'manifest.sha256').write_text(hashlib.sha256(raw).hexdigest() + '\n')
    with pytest.raises(ValueError):
        builder.verify(output)


def test_canonical_vectors_are_cross_language_bytes(builder):
    value = {'z': 0, 'a': ['<>&', 'é', '\n', True, None]}
    assert builder.canonical(value) == b'{"a":["<>&","\xc3\xa9","\\n",true,null],"z":0}\n'
    with pytest.raises(ValueError):
        builder.canonical({'float': 1.0})


@pytest.mark.parametrize('change', ['missing_required', 'nested_mapping_declaration'])
def test_build_refuses_profile_contract_drift(builder, source, tmp_path, change):
    path = source / 'project/conf/mappings/corrtest_operational_v1.json'
    rows = json.loads(path.read_bytes())
    if change == 'missing_required':
        for item in rows[0]['element']:
            if item['key'] == 'profile~required':
                item['value'] = 'labels.alertname,event_timestamp,ends_at'
    else:
        rows[0]['element'].append({'key': 'profile~mapping_namespaces', 'value': 'EXT_CUSTOM_ALERTS'})
    path.write_text(json.dumps(rows))
    with pytest.raises(ValueError):
        builder.build(source, BACKEND, tmp_path / 'out')
    assert not (tmp_path / 'out').exists()


def test_manifest_refuses_extra_directory(builder, tmp_path):
    output = tmp_path / 'out'
    builder.build(HERE / 'correlation-operational-v1', BACKEND, output)
    (output / 'unexpected').mkdir()
    with pytest.raises(ValueError):
        builder.verify(output)


@pytest.mark.parametrize('change', ['duplicate_yaml', 'server_managed_field'])
def test_rule_source_refuses_ambiguous_or_non_authored_fields(builder, source, tmp_path, change):
    path = source / 'correlation/flood.yml'
    if change == 'duplicate_yaml':
        path.write_text(path.read_text() + '\npriority: 100\n')
    else:
        path.write_text(path.read_text() + '\ncreated_by: hidden-author\n')
    with pytest.raises(ValueError):
        builder.build(source, BACKEND, tmp_path / 'out')
    assert not (tmp_path / 'out').exists()


@pytest.mark.parametrize('change', ['missing', 'oversized', 'symlink'])
def test_release_refuses_incomplete_unbounded_or_linked_files(builder, tmp_path, change):
    output = tmp_path / 'out'
    builder.build(HERE / 'correlation-operational-v1', BACKEND, output)
    path = output / 'correlation-rules.zip'
    if change == 'missing':
        path.unlink()
    elif change == 'oversized':
        path.write_bytes(b'x' * 1_048_577)
    else:
        other = tmp_path / 'other.zip'
        path.rename(other)
        path.symlink_to(other)
    with pytest.raises(ValueError):
        builder.verify(output)


def test_build_refuses_existing_output_without_modifying_it(builder, tmp_path):
    output = tmp_path / 'out'
    output.mkdir()
    (output / 'owner').write_bytes(b'untouched')
    with pytest.raises(ValueError):
        builder.build(HERE / 'correlation-operational-v1', BACKEND, output)
    assert list(output.iterdir()) == [output / 'owner']
    assert (output / 'owner').read_bytes() == b'untouched'


def test_pack_cannot_silently_change_declared_compatibility(builder, source, tmp_path):
    path = source / 'project/pack.source.yml'
    path.write_text(path.read_text().replace('>=26.3.0,<27.0.0', '>=26.3.0,<28.0.0'))
    with pytest.raises(ValueError):
        builder.build(source, BACKEND, tmp_path / 'out')
    assert not (tmp_path / 'out').exists()



def test_pack_metadata_and_payload_use_one_snapshot_despite_source_edit(builder, source, tmp_path, monkeypatch):
    profile_path = source / 'project/conf/mappings/corrtest_operational_v1.json'
    original_profile = json.loads(profile_path.read_bytes())[0]
    original_readme = (source / 'README.md').read_bytes()
    actual_call = builder.backend_call
    changed = []

    def edit_before_real_pack(backend, roots, code, data, *args, **kwargs):
        if code == builder.PACK_CODE:
            value = json.loads(profile_path.read_bytes())
            value[0]['description'] = 'changed after compiler validation'
            profile_path.write_text(json.dumps(value))
            (source / 'README.md').write_text('changed after validation')
            changed.append(True)
        return actual_call(backend, roots, code, data, *args, **kwargs)

    monkeypatch.setattr(builder, 'backend_call', edit_before_real_pack)
    output = tmp_path / 'out'
    manifest = builder.build(source, BACKEND, output)
    assert changed == [True]
    with tarfile.open(output / 'corrtest_operational-0.1.0.tar.gz') as archive:
        matches = [m for m in archive.getmembers() if m.name.endswith('/conf/mappings/corrtest_operational_v1.json')]
        assert len(matches) == 1
        packed_profile = json.load(archive.extractfile(matches[0]))[0]
    assert hashlib.sha256(builder.canonical(packed_profile)).hexdigest() == manifest['profile']['mapping_sha256']
    assert packed_profile == original_profile
    assert (output / 'README.md').read_bytes() == original_readme
    builder.verify(output)


def test_actual_file_read_is_bounded_even_if_size_metadata_is_stale(builder, tmp_path, monkeypatch):
    path = tmp_path / 'growing-input'
    path.write_bytes(b'x' * 1_048_577)
    actual_stat = Path.stat

    def stale_stat(self, *args, **kwargs):
        result = actual_stat(self, *args, **kwargs)
        if self == path:
            fields = list(result)
            fields[6] = 1
            return os.stat_result(fields)
        return result

    monkeypatch.setattr(Path, 'stat', stale_stat)
    with pytest.raises(ValueError):
        builder.read(path)


def test_real_loader_normalization_preserves_effective_receiver_hash(builder, tmp_path):
    path = HERE / 'correlation-operational-v1/project/conf/mappings/corrtest_operational_v1.json'
    row = json.loads(path.read_bytes())[0]
    code = '''
import json, sys
from app.core.pack_artifacts import _assign_mapping, desired_mapping_state
from app.core.db import Mapping
from tm.schemas.mapping_data import MappingCreate
row = json.load(sys.stdin)
request = MappingCreate.model_validate(row)
stored = Mapping()
_assign_mapping(stored, request)
normalized = desired_mapping_state(row)
assert normalized["description"] == stored.description
assert normalized["name"] == stored.name
print(json.dumps(normalized))
'''
    normalized = builder.backend_call(BACKEND, ['oscar-taskmanager/src', 'oscar-taskmanager/src/app', 'oscar-util/scripts'], code,
                                      builder.canonical(row), label='real Mapping loader')
    loaded = tmp_path / 'loaded-profile.json'
    loaded.write_text(json.dumps([normalized]))
    authored_result = builder.validate_profile(BACKEND, path, builder.sample_body())
    loaded_result = builder.validate_profile(BACKEND, loaded, builder.sample_body())
    assert loaded_result['effective_sha256'] == authored_result['effective_sha256']
