"""Fixed installer controls, with actual public schema conformance checks.

The HTTP peer and local command are controlled boundaries, not deployed proof.
"""
import hashlib
import importlib.util
import json
from pathlib import Path
import uuid
import tarfile

import httpx
import pytest

import build_operational as kit

HERE = Path(__file__).resolve().parent
BACKEND = HERE.parents[1] / 'oscar'
ORIGIN = 'https://owned-target.test'
TOKEN = 'test-credential-never-in-evidence'
LOAD_ID = '00000000-0000-4000-8000-000000000001'


@pytest.fixture(scope='module')
def installer():
    path = HERE / 'install_operational.py'
    assert path.is_file(), 'fixed durable installer is not implemented'
    spec = importlib.util.spec_from_file_location('operational_installer', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture(scope='module')
def distribution(tmp_path_factory):
    output = tmp_path_factory.mktemp('kit') / 'dist'
    kit.build(HERE / 'correlation-operational-v1', BACKEND, output)
    return output


class Peer:
    def __init__(self, mod, distribution, evidence):
        self.mod, self.evidence = mod, evidence
        self.bundle = mod.Bundle(distribution, kit.digest((distribution / 'manifest.json').read_bytes()))
        self.calls = []
        self.installed = self.loaded = self.imported = False
        self.failure = None
        self.schema_samples = {}

    def sample(self, name, value):
        self.schema_samples[name] = value
        return value

    def runtime(self, selected=False):
        return {'checked_at_ms': 1792200000000, 'load_status': 'loaded', 'last_attempt_ms': 1792200000000,
                'loaded_at': '2026-10-17T00:00:00+00:00', 'loaded_rule_ids': [7] if self.imported else [],
                'loaded_total': int(self.imported), 'loaded_omitted': 0, 'rejected_rules': [], 'rejected_total': 0,
                'rejected_omitted': 0, 'selected_rule': {'id': 7, 'loaded': True, 'rejection': None} if selected else None,
                'dispatch_enabled': False, 'authoritative': self.failure == 'authoritative',
                'breaker': {'state': 'disabled', 'fresh': True, 'observed_at_ms': 1792200000000},
                'dependencies': {'status': 'ready', 'nats': True, 'consumer': True, 'redis': self.failure != 'redis',
                                 'lua_registered': True, 'timer_store': True},
                'enhanced_correlation': {}, 'operational': False}

    def rule(self):
        row = {**self.bundle.rule, 'id': 7, 'created_by': 'owned-operator',
               'created_at': '2026-10-17T00:00:00+00:00', 'updated_at': '2026-10-17T00:00:00+00:00'}
        if self.failure == 'rule_drift':
            row['window_seconds'] = 61
        return row

    def tool(self, command, archive, sha, token, remaining):
        assert token == TOKEN
        assert archive.parent == self.evidence / 'kit'
        assert hashlib.sha256(archive.read_bytes()).hexdigest() == sha
        before = list(self.evidence.glob('*-before.json'))
        assert any(json.loads(path.read_text()).get('operation') == 'project-install' for path in before)
        self.calls.append(('TOOL', 'project-install'))
        self.installed = True
        if self.failure == 'tool_lost':
            raise TimeoutError(TOKEN)
        return {'status': 'complete', 'operation_id': str(uuid.uuid4()), 'command': 'install', 'pack_id': kit.ID,
                'old_version': None, 'new_version': kit.VERSION, 'manifest_sha256': self.bundle.pack_manifest_sha,
                'inserted': 0, 'updated': 0, 'disabled': 0, 'purged': 0, 'schedules_changed': 0,
                'caches_invalidated': True, 'renamed': 0, 'preapplied': 0, 'missing': 0, 'quarantined': 0}

    def handle(self, request):
        assert str(request.url).startswith(ORIGIN + '/')
        assert request.headers['X-API-Key'] == TOKEN
        path = request.url.path
        self.calls.append((request.method, path))
        prefix = '/api/v1/packs/' + kit.ID
        if path == '/api/v1/packs':
            rows = [{'pack_id': kit.ID, 'display_name': kit.ID, 'pack_version': kit.VERSION,
                     'manifest_status': 'valid', 'pack_content_hash': self.bundle.content_hash,
                     'counts': {'rule': 0, 'mapping': 1, 'role': 0, 'dashboard': 0}, 'latest_load': None}] if self.installed or self.failure == 'foreign_pack' else []
            value = self.sample('InstalledPackList', {'packs': rows})
        elif path == '/api/v1/correlation_rules/runtime':
            value = self.runtime('rule_id' in request.url.params)
        elif path == '/api/v1/correlation_rules/validate':
            assert json.loads(request.content) == self.bundle.rule
            value = self.sample('CorrelationRuleValidateResponse', {'valid': True, 'errors': [], 'policy_preview': None})
        elif path == '/api/v1/correlation_rules':
            rows = [self.rule()] if self.imported or self.failure == 'foreign_rule' else []
            value = self.sample('CorrelationRuleListResponse', {'rows': rows, 'total': len(rows), 'page': 1, 'perPage': 100})
        elif path.endswith('/configuration/preview'):
            assert self.installed
            item = {**self.bundle.artifact, 'row_key': self.bundle.row_key,
                    'source_file': self.bundle.mapping_path, 'source_ordinal': 0, 'action': 'create',
                    'requires_precondition': False, 'diagnostic': None, 'expected_live_hash': None,
                    'state': 'new', 'pack_hash': self.bundle.mapping_state_sha, 'live_hash': None,
                    'applied_hash': None, 'changed_fields': []}
            if self.failure == 'foreign_mapping':
                item.update(action='conflict', state='conflict', live_hash='a' * 64)
            value = self.sample('PackPreview', {'pack_id': kit.ID, 'pack_version': kit.VERSION,
                    'pack_content_hash': self.bundle.content_hash, 'counts': {'rule': 0, 'mapping': 1, 'role': 0, 'dashboard': 0}, 'items': [item]})
        elif path.endswith('/configuration/loads'):
            payload = json.loads(request.content)
            assert payload == {'pack_content_hash': self.bundle.content_hash, 'overwrite': [],
                               'preconditions': [{'artifact': self.bundle.artifact, 'expected_live_hash': None}]}
            self.schema_samples['PackLoadRequest'] = payload
            self.loaded = True
            if self.failure == 'load_lost':
                raise httpx.ReadTimeout(TOKEN)
            value = self.sample('PackLoadAccepted', {'load_id': LOAD_ID, 'status': 'queued',
                'status_url': 'https://foreign.test/stolen' if self.failure == 'foreign_status' else prefix + '/configuration/loads/' + LOAD_ID})
        elif '/configuration/loads/' in path:
            assert dict(request.url.params) == {'page': '1', 'per_page': '100'}
            value = {'load_id': LOAD_ID, 'pack_id': kit.ID, 'status': 'completed',
                     'activation_status': 'failed' if self.failure == 'activation' else 'succeeded',
                     'activation_retryable': False, 'created_count': 1, 'replaced_count': 0,
                     'skipped_count': 0, 'failed_count': 0,
                     'activation_targets': [{'artifact_type': 'mapping', 'namespace': 'webhook_profiles'}],
                     'activation_errors': [], 'items': [{**self.bundle.artifact, 'id': 1, 'source_file': self.bundle.mapping_path,
                      'source_ordinal': 0, 'intended_action': 'create', 'result': 'created',
                      'external_id': LOAD_ID, 'applied_hash': self.bundle.mapping_state_sha,
                      'error_code': None, 'diagnostic': None, 'started_at': None, 'completed_at': '2026-10-17T00:00:00+00:00'}],
                     'page': 1, 'per_page': 100, 'total': 1}
            if self.failure == 'partial_load':
                value['total'] = 2
            value = self.sample('PackLoadStatus', value)
        elif '/configuration/items/' in path:
            value = self.sample('PackItemComparison', {'row_key': self.bundle.row_key, **self.bundle.artifact,
                'state': 'up_to_date', 'pack': self.bundle.mapping_state, 'live': self.bundle.mapping_state,
                'pack_hash': self.bundle.mapping_state_sha, 'live_hash': self.bundle.mapping_state_sha,
                'applied_hash': self.bundle.mapping_state_sha, 'changed_fields': [], 'diagnostic': None})
        elif path == '/api/v1/correlation_rules/import':
            assert self.loaded
            assert b'correlation-rules.zip' in request.content
            self.imported = True
            if self.failure == 'import_lost':
                raise httpx.ReadTimeout(TOKEN)
            value = {'created': [kit.RULE], 'updated': [], 'errors': []}
            if self.failure == 'partial_import':
                value['errors'] = [{'file': 'flood.yml', 'error': 'rejected'}]
            if self.failure == 'updated':
                value.update(created=[], updated=[kit.RULE])
            value = self.sample('CorrelationRuleImportResult', value)
        elif path == '/api/v1/correlation_rules/7':
            value = self.sample('CorrelationRuleResponse', self.rule())
        else:
            raise AssertionError((request.method, str(request.url)))
        return httpx.Response(200 if not path.endswith('/configuration/loads') else 202, json=value)


@pytest.fixture
def setup(installer, distribution, tmp_path, monkeypatch):
    evidence = tmp_path / 'attempt'
    peer = Peer(installer, distribution, evidence)
    monkeypatch.setattr(installer, '_project_install', peer.tool)
    ownership = {'format': 'corrtest-owned-target-v1', 'target': ORIGIN, 'owner_reference': 'root-owned-target-1',
                 'exclusive_through_export': True, 'stable_configuration_and_processes': True,
                 'destinations_history_or_controlled_lab': True}
    return peer, ownership, evidence


def install(mod, distribution, peer, ownership, evidence):
    return mod.install(distribution, kit.digest((distribution / 'manifest.json').read_bytes()), ORIGIN,
                       TOKEN, Path('/owned/oscar'), ownership, evidence, transport=httpx.MockTransport(peer.handle))


def test_completed_install_and_verify_are_bound_and_verify_is_get_only(installer, distribution, setup):
    peer, ownership, evidence = setup
    result = install(installer, distribution, peer, ownership, evidence)
    assert result['status'] == 'completed'
    assert result['rule_id'] == 7 and result['load_id'] == LOAD_ID
    assert (evidence.stat().st_mode & 0o777) == 0o700
    assert all((p.stat().st_mode & 0o777) == 0o600 for p in evidence.glob('*.json'))
    assert not any(TOKEN.encode() in p.read_bytes() for p in evidence.rglob('*') if p.is_file())
    peer.calls.clear()
    installer.verify_install(evidence, result['manifest_sha256'], ORIGIN, TOKEN, ownership,
                             transport=httpx.MockTransport(peer.handle))
    assert peer.calls and all(method == 'GET' for method, _ in peer.calls)


@pytest.mark.parametrize('failure', ['foreign_pack', 'foreign_rule', 'authoritative', 'redis', 'foreign_mapping',
                                    'foreign_status', 'partial_load', 'activation', 'partial_import', 'updated',
                                    'rule_drift', 'tool_lost', 'load_lost', 'import_lost'])
def test_failure_never_retries_adopts_or_claims_completed(installer, distribution, setup, failure):
    peer, ownership, evidence = setup
    peer.failure = failure
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert not (evidence / 'completed.json').exists()
    if failure in {'partial_import', 'updated', 'rule_drift', 'import_lost'}:
        assert peer.imported, 'control must reach the intended import boundary'
    elif failure in {'foreign_status', 'partial_load', 'activation', 'load_lost'}:
        assert peer.loaded, 'control must reach the intended load boundary'
    assert not any(method in ('PUT', 'DELETE') for method, _ in peer.calls)
    for method, suffix in [('TOOL', 'project-install'), ('POST', '/api/v1/correlation_rules/import'),
                           ('POST', '/api/v1/packs/' + kit.ID + '/configuration/loads')]:
        assert peer.calls.count((method, suffix)) <= 1
    assert not any(TOKEN.encode() in p.read_bytes() for p in evidence.rglob('*') if p.is_file())
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)


def test_record_failure_prevents_local_mutation(installer, distribution, setup, monkeypatch):
    peer, ownership, evidence = setup
    original = installer.Journal.before
    def refuse(self, operation, *args, **kwargs):
        if operation == 'project-install':
            raise OSError('controlled fsync failure')
        return original(self, operation, *args, **kwargs)
    monkeypatch.setattr(installer.Journal, 'before', refuse)
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert not peer.installed


def test_missing_external_ownership_refuses_before_calls(installer, distribution, setup):
    peer, ownership, evidence = setup
    ownership['exclusive_through_export'] = False
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert peer.calls == []


def test_controlled_success_shapes_conform_to_actual_public_schemas(installer, distribution, setup):
    peer, ownership, evidence = setup
    install(installer, distribution, peer, ownership, evidence)
    code = '''
import json,sys
from app.schemas import packs, correlation_rules
rows=json.load(sys.stdin)
for name,value in rows.items():
    cls=getattr(packs,name,None) or getattr(correlation_rules,name)
    cls.model_validate(value)
print(json.dumps({"validated": sorted(rows)}))
'''
    result = kit.backend_call(BACKEND, ['oscar-middleware/src', 'oscar-middleware/src/app'], code,
                              kit.canonical(peer.schema_samples), label='public schema')
    assert len(result['validated']) >= 9


def test_expected_mapping_state_matches_real_loader(installer, distribution, setup):
    peer, _, _ = setup
    code = '''
import json,sys
from app.core.pack_artifacts import desired_mapping_state,mapping_payload_with_source_pack,canonical_payload_hash
row=json.load(sys.stdin)
state=desired_mapping_state(mapping_payload_with_source_pack(row,"corrtest_operational"))
print(json.dumps({"state":state,"sha256":canonical_payload_hash(state)}))
'''
    actual = kit.backend_call(BACKEND, ['oscar-taskmanager/src', 'oscar-taskmanager/src/app', 'oscar-util/scripts'], code,
                              kit.canonical(peer.bundle.profile), label='actual loader state')
    assert actual == {'state': peer.bundle.mapping_state, 'sha256': peer.bundle.mapping_state_sha}


@pytest.mark.parametrize('mutation', ['runtime_reload', 'mapping', 'rule', 'target', 'incomplete_load'])
def test_verify_cannot_refresh_original_completion(installer, distribution, setup, mutation):
    peer, ownership, evidence = setup
    result = install(installer, distribution, peer, ownership, evidence)
    original = {p.relative_to(evidence): p.read_bytes() for p in evidence.rglob('*') if p.is_file()}
    handle = peer.handle
    def changed(request):
        response = handle(request)
        body = response.json()
        path = request.url.path
        if mutation == 'runtime_reload' and path.endswith('/runtime'):
            body['last_attempt_ms'] += 1
        elif mutation == 'mapping' and '/configuration/items/' in path:
            body['live']['description'] = 'foreign'
        elif mutation == 'rule' and path.endswith('/7'):
            body['window_seconds'] += 1
        elif mutation == 'incomplete_load' and '/configuration/loads/' in path:
            body['status'] = 'running'
        return httpx.Response(200, json=body)
    peer.calls.clear()
    with pytest.raises(installer.InstallError):
        installer.verify_install(evidence, result['manifest_sha256'], ORIGIN + ('.foreign' if mutation == 'target' else ''),
                                 TOKEN, ownership, transport=httpx.MockTransport(changed))
    assert all(method == 'GET' for method, _ in peer.calls)
    assert original == {p.relative_to(evidence): p.read_bytes() for p in evidence.rglob('*') if p.is_file()}


@pytest.mark.parametrize('bad', ['redirect', 'oversize', 'duplicate_json', 'partial_rules', 'phase_b_dispatch_off'])
def test_preflight_refuses_malformed_or_unavailable_before_mutation(installer, distribution, setup, bad):
    peer, ownership, evidence = setup
    handle = peer.handle
    def response(request):
        if bad == 'redirect':
            peer.calls.append((request.method, request.url.path))
            return httpx.Response(307, headers={'Location': 'https://foreign.test/steal'})
        if bad == 'oversize':
            return httpx.Response(200, content=b' ' * (installer.HTTP_LIMIT + 1))
        if bad == 'duplicate_json':
            return httpx.Response(200, content=b'{"packs":[],"packs":[]}')
        result = handle(request)
        body = result.json()
        if bad == 'partial_rules' and request.url.path.endswith('/correlation_rules'):
            body['total'] = 1
        if bad == 'phase_b_dispatch_off' and request.url.path.endswith('/runtime'):
            body.update(authoritative=True, dispatch_enabled=False)
        return httpx.Response(200, json=body)
    with pytest.raises(installer.InstallError):
        installer.install(distribution, peer.bundle.manifest_sha, ORIGIN, TOKEN, Path('/owned/oscar'), ownership,
                          evidence, transport=httpx.MockTransport(response))
    assert not peer.installed and not peer.loaded and not peer.imported
    assert all(method == 'GET' for method, _ in peer.calls)


@pytest.mark.parametrize('suffix', ['/configuration/loads', '/correlation_rules/import'])
def test_http_mutation_cannot_outlive_failed_before_record(installer, distribution, setup, monkeypatch, suffix):
    peer, ownership, evidence = setup
    before = installer.Journal.before
    def fail(self, operation, request):
        if operation == 'http' and request['method'] == 'POST' and request['path'].endswith(suffix):
            raise OSError('fsync refused')
        return before(self, operation, request)
    monkeypatch.setattr(installer.Journal, 'before', fail)
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert not any(method == 'POST' and path.endswith(suffix) for method, path in peer.calls)
    assert not (evidence / 'completed.json').exists()


def test_local_command_is_single_existing_cli_and_private_environment(installer, tmp_path):
    command = tmp_path / 'oscar'
    command.write_text('#!/usr/bin/env python3\nimport json,os,sys\n'
                       'assert os.environ["OSCAR_CLI_TOKEN"] == "test-credential-never-in-evidence"\n'
                       'print(json.dumps({"argv":sys.argv[1:]}))\n')
    command.chmod(0o700)
    archive = tmp_path / kit.ARCHIVE
    archive.write_bytes(b'controlled archive')
    value = installer._project_install(command, archive, 'a' * 64, TOKEN, 5)
    assert value == {'argv': ['project', 'install', str(archive.resolve()), '--sha256', 'a' * 64, '--json']}


@pytest.mark.parametrize('mode', ['timeout', 'overflow', 'nonzero'])
def test_local_command_failure_is_bounded_unknown(installer, tmp_path, mode):
    command = tmp_path / 'oscar'
    body = {'timeout': 'import time\ntime.sleep(10)', 'overflow': 'print("x" * 300000)', 'nonzero': 'raise SystemExit(4)'}[mode]
    command.write_text('#!/usr/bin/env python3\n' + body + '\n')
    command.chmod(0o700)
    with pytest.raises(installer.InstallError):
        installer._project_install(command, tmp_path / kit.ARCHIVE, 'a' * 64, TOKEN, 0.1 if mode == 'timeout' else 5)


def test_response_cannot_echo_secret_in_evidence_keys(installer, distribution, setup):
    peer, ownership, evidence = setup
    handle = peer.handle
    def echo(request):
        response = handle(request)
        value = response.json()
        if request.url.path == '/api/v1/packs':
            value[TOKEN] = 'echoed-as-key'
        return httpx.Response(response.status_code, json=value)
    installer.install(distribution, peer.bundle.manifest_sha, ORIGIN, TOKEN, Path('/owned/oscar'), ownership,
                      evidence, transport=httpx.MockTransport(echo))
    assert not any(TOKEN.encode() in p.read_bytes() for p in evidence.rglob('*') if p.is_file())


@pytest.mark.parametrize('field', ['loaded_omitted', 'rejected_total', 'rejected_omitted'])
def test_runtime_missing_count_types_do_not_prove_complete_inventory(installer, distribution, setup, field):
    peer, ownership, evidence = setup
    original = peer.runtime
    def bad(selected=False):
        return {**original(selected), field: False}
    peer.runtime = bad
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert not peer.installed


def test_single_distribution_contains_hash_bound_installer_and_deterministic_release(distribution, tmp_path):
    manifest = kit.verify(distribution)
    by_name = {entry['path']: entry for entry in manifest['files']}
    for name in ('install_operational.py', 'build_operational.py'):
        assert by_name[name]['sha256'] == kit.digest((HERE / name).read_bytes())
        assert (distribution / name).read_bytes() == (HERE / name).read_bytes()
    first, second = tmp_path / 'first.tar.gz', tmp_path / 'second.tar.gz'
    kit.release(distribution, first)
    kit.release(distribution, second)
    assert first.read_bytes() == second.read_bytes()
    with tarfile.open(first, 'r:gz') as archive:
        assert sorted(archive.getnames()) == ['correlation-operational-v1/' + path.name for path in sorted(distribution.iterdir())]
        assert all(member.isfile() and member.size <= kit.MAX_BYTES for member in archive)
        for member in archive:
            assert archive.extractfile(member).read() == (distribution / Path(member.name).name).read_bytes()
    with pytest.raises(ValueError):
        kit.release(distribution, first)


@pytest.mark.parametrize('boundary', ['load', 'import', 'completion'])
def test_observed_mutation_with_lost_evidence_never_reopens_or_retries(installer, distribution, setup, monkeypatch, boundary):
    peer, ownership, evidence = setup
    original = installer.Journal.save
    def fail(self, name, value):
        at_boundary = (boundary == 'load' and name.endswith('-after.json') and peer.loaded
                       or boundary == 'import' and name.endswith('-after.json') and peer.imported
                       or boundary == 'completion' and name == 'completed.json')
        if at_boundary:
            raise OSError('controlled post-send persistence failure')
        return original(self, name, value)
    monkeypatch.setattr(installer.Journal, 'save', fail)
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert peer.loaded and (boundary == 'load' or peer.imported)
    assert not (evidence / 'completed.json').exists()
    assert json.loads((evidence / 'failed.json').read_text())['status'] == 'INCONCLUSIVE'
    original_calls = list(peer.calls)
    with pytest.raises(installer.InstallError):
        install(installer, distribution, peer, ownership, evidence)
    assert peer.calls == original_calls


@pytest.mark.parametrize('change', [{'scope': 'scenario-PASS'}, {'profile_revision_observed': True},
                                    {'ownership_observed': True}, {'extra': 'not-original-contract'}])
def test_verify_rejects_changed_receipt_contract_before_requests(installer, distribution, setup, change):
    peer, ownership, evidence = setup
    result = install(installer, distribution, peer, ownership, evidence)
    path = evidence / 'completed.json'
    path.write_bytes(kit.canonical({**result, **change}))
    peer.calls.clear()
    with pytest.raises(installer.InstallError):
        installer.verify_install(evidence, result['manifest_sha256'], ORIGIN, TOKEN, ownership,
                                 transport=httpx.MockTransport(peer.handle))
    assert not peer.calls


@pytest.mark.parametrize('damage', ['operation_id', 'missing_project_response', 'missing_load_response',
                                  'missing_import_response', 'gap', 'extra_mutation', 'changed_load_id',
                                  'changed_rule_id', 'changed_runtime', 'changed_response'])
def test_verify_requires_original_complete_mutation_journal_before_http(installer, distribution, setup, damage):
    peer, ownership, evidence = setup
    completed = install(installer, distribution, peer, ownership, evidence)
    if damage in {'operation_id', 'changed_load_id', 'changed_rule_id', 'changed_runtime'}:
        field = {'operation_id': 'project_operation_id', 'changed_load_id': 'load_id',
                 'changed_rule_id': 'rule_id', 'changed_runtime': 'runtime_binding'}[damage]
        completed[field] = (str(uuid.uuid4()) if field.endswith('_id') and field != 'rule_id'
                            else 8 if field == 'rule_id' else {**completed[field], 'last_attempt_ms': 1})
        (evidence / 'completed.json').write_bytes(kit.canonical(completed))
    else:
        for before in sorted(evidence.glob('*-before.json')):
            entry = json.loads(before.read_text())
            after = before.with_name(before.name.replace('-before.json', '-after.json'))
            match = (damage == 'missing_project_response' and entry['operation'] == 'project-install'
                     or damage == 'missing_load_response' and entry['request'].get('path', '').endswith('/configuration/loads')
                     or damage == 'missing_import_response' and entry['request'].get('path', '').endswith('/import'))
            if match:
                after.unlink()
                break
        if damage == 'gap':
            next(evidence.glob('*-before.json')).unlink()
        if damage == 'extra_mutation':
            (evidence / '0161-before.json').write_bytes(kit.canonical({'operation': 'project-install'}))
        if damage == 'changed_response':
            path = next(evidence.glob('*-after.json'))
            path.write_bytes(kit.canonical({'status': 200, 'body': {'packs': []}}))
    peer.calls.clear()
    with pytest.raises(installer.InstallError):
        installer.verify_install(evidence, peer.bundle.manifest_sha, ORIGIN, TOKEN, ownership,
                                 transport=httpx.MockTransport(peer.handle))
    assert peer.calls == [], 'original journal must be bound before current readback'


@pytest.mark.parametrize('damage', ['project_operation', 'accepted_load', 'updated_import', 'duplicate_project', 'final_runtime'])
def test_rehashed_journal_still_requires_original_cross_record_bindings(installer, distribution, setup, damage):
    peer, ownership, evidence = setup
    completed = install(installer, distribution, peer, ownership, evidence)
    project = None
    for before_path in sorted(evidence.glob('*-before.json')):
        before = json.loads(before_path.read_text())
        after_path = before_path.with_name(before_path.name.replace('-before.json', '-after.json'))
        after = json.loads(after_path.read_text())
        if before['operation'] == 'project-install':
            project = before, after
            if damage == 'project_operation':
                after['receipt']['operation_id'] = str(uuid.uuid4())
                after_path.write_bytes(kit.canonical(after))
        path = before['request'].get('path', '')
        if damage == 'accepted_load' and before['request'].get('method') == 'POST' and path.endswith('/configuration/loads'):
            after['body']['load_id'] = str(uuid.uuid4())
            after_path.write_bytes(kit.canonical(after))
        if damage == 'updated_import' and path.endswith('/import'):
            after['body'] = {'created': [], 'updated': [kit.RULE], 'errors': []}
            after_path.write_bytes(kit.canonical(after))
        if damage == 'final_runtime' and path.endswith('/runtime?rule_id=7'):
            after['body']['last_attempt_ms'] += 1
            after_path.write_bytes(kit.canonical(after))
    if damage == 'duplicate_project':
        assert project is not None
        (evidence / '0001-before.json').write_bytes(kit.canonical(project[0]))
        (evidence / '0001-after.json').write_bytes(kit.canonical(project[1]))
    _, completed['journal_sha256'] = installer._journal_image(evidence, completed['journal_operations'], completed=True)
    (evidence / 'completed.json').write_bytes(kit.canonical(completed))
    peer.calls.clear()
    with pytest.raises(installer.InstallError):
        installer.verify_install(evidence, peer.bundle.manifest_sha, ORIGIN, TOKEN, ownership,
                                 transport=httpx.MockTransport(peer.handle))
    assert not peer.calls
