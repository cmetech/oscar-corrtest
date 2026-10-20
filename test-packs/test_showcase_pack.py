"""Finite showcase artifacts validated by real backend compilers, without deployment."""
import importlib.util
import json
from pathlib import Path
import zipfile

import pytest
import yaml


HERE = Path(__file__).resolve().parent
BACKEND = HERE.parents[1] / 'oscar'


@pytest.fixture(scope='module')
def builder():
    path = HERE / 'build_showcase.py'
    assert path.is_file(), 'finite showcase builder is not implemented'
    spec = importlib.util.spec_from_file_location('showcase_builder', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_showcase_build_validates_seven_disabled_rules_and_retains_full_path_requirements(builder, tmp_path):
    manifest = builder.build(BACKEND, tmp_path / 'kit')
    assert manifest['execution_authority'] is False
    assert manifest['profile']['name'] == 'corrtest_showcase_v2'
    assert manifest['retained_coexistence'] == ['corrtest-operational-v1-flood']
    assert manifest['requires'] == builder.REQUIRES
    assert 'physical-parent-history-not-audit-only' in manifest['requires']
    assert 'history-only-controlled-destinations' in manifest['requires']
    assert 'coordinated-ordinary-authoritative-dispatch-readback' in manifest['requires']
    with zipfile.ZipFile(tmp_path / 'kit/correlation-rules.zip') as archive:
        rules = [yaml.safe_load(archive.read(name)) for name in archive.namelist()]
    assert {rule['pattern'] for rule in rules} == {
        'flood', 'co_occurrence', 'sequence', 'threshold', 'cross_source', 'persistence', 'parent_child'}
    for rule in rules:
        assert rule['enabled'] is False
        assert rule['correlation_policy'] is None
        assert rule['group_by_labels'] == ['qualification_run', 'site']
        if rule['pattern'] != 'parent_child':
            assert rule['emit_spec']['severity'] == 'critical'
        else:
            assert rule['emit_spec'] is None
            assert not rule['match_criteria']['suppress_children_for_notifiers']
    persistence = next(rule for rule in rules if rule['pattern'] == 'persistence')
    assert persistence['match_criteria']['unresolved_for_seconds'] == 30
    assert 'min_source_occurrences' not in persistence['match_criteria']
    assert builder.verify(tmp_path / 'kit') == manifest


def test_real_showcase_profile_preserves_source_device_and_drops_control_fields(builder):
    body = builder.sample_body()
    body['labels'].update(source_occurrences='999', oscar_correlation_rule_id='forged')
    result = builder.validate_profile(BACKEND, body)
    assert result['labels']['device'] == 'device-1'
    assert result['labels']['oscar_source'] == 'snmp'
    assert 'source_occurrences' not in result['labels']
    assert 'oscar_correlation_rule_id' not in result['labels']


@pytest.mark.parametrize('key', ['qualification_run', 'source_id', 'site', 'device', 'oscar_source'])
def test_real_showcase_profile_rejects_missing_identity_or_pattern_fields(builder, key):
    body = builder.sample_body()
    del body['labels'][key]
    with pytest.raises(ValueError, match='profile validation'):
        builder.validate_profile(BACKEND, body)


def test_showcase_contract_has_positive_negative_and_no_physical_clear_commands(builder):
    cases = builder.scenario()['cases']
    assert len(cases) == 14
    assert {case['pattern'] for case in cases} == set(builder.PATTERNS)
    for pattern in builder.PATTERNS:
        selected = [case for case in cases if case['pattern'] == pattern]
        assert {case['polarity'] for case in selected} == {'positive', 'negative'}
        assert all(case['expected_parent_history'] == (1 if case['polarity'] == 'positive' and pattern != 'parent_child' else 0) for case in selected)
    assert all(event['status'] == 'firing' for case in cases for event in case['inputs'])
    assert all(case['observe_seconds'] >= 90 for case in cases)
    assert builder.scenario()['cleanup'] == 'retained-dirty-no-automatic-clear-or-delete'


def test_showcase_verifier_refuses_rebound_artifact_and_overwrite(builder, tmp_path):
    root = tmp_path / 'kit'
    builder.build(BACKEND, root)
    with pytest.raises(ValueError, match='exists'):
        builder.build(BACKEND, root)
    scenario = json.loads((root / 'showcase.json').read_bytes())
    scenario['cases'][0]['expected_parent_history'] = 0
    (root / 'showcase.json').write_text(json.dumps(scenario))
    with pytest.raises(ValueError):
        builder.verify(root)
