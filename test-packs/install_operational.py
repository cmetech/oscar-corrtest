#!/usr/bin/env python3
"""Finite target-side installation; no retry, rollback, source input or run proof."""
import argparse
import io
import json
import os
from pathlib import Path
import re
import selectors
import signal
import ssl
import stat
import subprocess
import time
from urllib.parse import urlsplit
from uuid import UUID, uuid4
import tarfile
import zipfile

import httpx
import yaml

import build_operational as kit

PREFIX = '/api/v1/packs/' + kit.ID
RULES = '/api/v1/correlation_rules'
HTTP_LIMIT = 262_144
DEADLINE_SECONDS = 300
COMPLETED_FIELDS = {'format', 'status', 'target', 'manifest_sha256', 'pack_content_hash',
                    'project_operation_id', 'load_id', 'rule_id', 'profile_effective_sha256',
                    'profile_revision_observed', 'authored_rule_sha256', 'runtime_binding',
                    'ownership_declaration', 'ownership_observed', 'limits', 'scope',
                    'journal_operations', 'journal_sha256'}
OWNERSHIP_KEYS = {'format', 'target', 'owner_reference', 'exclusive_through_export',
                  'stable_configuration_and_processes', 'destinations_history_or_controlled_lab'}
LIMITS = ['ownership is operator-declared, not machine-verified exclusion',
          'readback cannot detect change/use/restore between requests',
          'runtime API supplies no immutable process identity',
          'installation verification is not scenario PASS or source cleanup proof',
          'offline expected receiver revision requires If-Match and acknowledgement validation during input']


class InstallError(ValueError):
    pass


def require(condition, reason):
    if not condition:
        raise InstallError(reason)


def canonical_hash(value):
    # Backend pack identity/state hashing has no trailing LF.
    return kit.digest(kit.canonical(value)[:-1])


def positive(value):
    return type(value) is int and 0 < value <= 2**53 - 1


def origin(value, allow_owned_http=False):
    parsed = urlsplit(value)
    require(parsed.scheme == 'https' or (parsed.scheme == 'http' and allow_owned_http), 'HTTPS required')
    require(parsed.hostname and not parsed.username and not parsed.password and not parsed.query and not parsed.fragment
            and parsed.path in ('', '/', '/ext/mw') and '%' not in parsed.netloc and '%' not in parsed.path
            and '?' not in value and '#' not in value and '\\' not in value
            and not any(c.isspace() for c in value), 'invalid target origin')
    try:
        _ = parsed.port  # Access validates malformed or out-of-range ports.
    except ValueError as error:
        raise InstallError('invalid target port') from error
    return value[:-1] if parsed.path == '/' else value


def ownership_check(value, target):
    require(type(value) is dict and set(value) == OWNERSHIP_KEYS, 'explicit external ownership declaration required')
    require(value['format'] == 'corrtest-owned-target-v1' and value['target'] == target, 'ownership target mismatch')
    require(type(value['owner_reference']) is str and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}', value['owner_reference']),
            'bounded external owner reference required')
    for name in ('exclusive_through_export', 'stable_configuration_and_processes', 'destinations_history_or_controlled_lab'):
        require(value[name] is True, 'unsupported without external ' + name)


def _uuid(value):
    require(type(value) is str, 'canonical UUID required')
    try:
        parsed = UUID(value)
    except ValueError as error:
        raise InstallError('canonical UUID required') from error
    require(str(parsed) == value and parsed.version == 4, 'canonical UUID4 required')
    return value


def _private_json(path):
    require(stat.S_IMODE(path.lstat().st_mode) == 0o600, 'private evidence file required')
    return kit.strict_json(kit.read(path))


class Bundle:
    """Verified fixed distribution bytes; no target authority is derived here."""
    def __init__(self, directory, expected_sha):
        self.directory = Path(directory)
        require(type(expected_sha) is str and kit.DIGEST.fullmatch(expected_sha), 'pinned manifest SHA required')
        self.manifest = kit.verify(self.directory)
        require(kit.digest(kit.read(self.directory / 'manifest.json')) == expected_sha, 'manifest pin mismatch')
        self.manifest_sha = expected_sha
        self.archive = kit.read(self.directory / kit.ARCHIVE)
        self.mapping_path = 'conf/mappings/' + kit.PROFILE + '.json'
        expected = {kit.ID + '/pack.yml', kit.ID + '/' + self.mapping_path}
        regular = {}
        with tarfile.open(fileobj=io.BytesIO(self.archive), mode='r:gz') as archive:
            for index, member in enumerate(archive):
                require(index < 8, 'too many inner members')
                if member.isdir():
                    require(member.name in {kit.ID, kit.ID + '/conf', kit.ID + '/conf/mappings'}, 'unexpected inner directory')
                    continue
                require(member.isfile() and member.name in expected and member.name not in regular
                        and 0 < member.size <= kit.MAX_BYTES, 'invalid inner member')
                stream = archive.extractfile(member)
                body = stream.read(kit.MAX_BYTES + 1)
                require(len(body) == member.size, 'inner size mismatch')
                regular[member.name] = body
        require(set(regular) == expected, 'incomplete inner pack')
        pack_bytes = regular[kit.ID + '/pack.yml']
        profile_bytes = regular[kit.ID + '/' + self.mapping_path]
        self.pack_manifest_sha = kit.digest(pack_bytes)
        self.content_hash = 'sha256:' + kit.digest(pack_bytes + self.mapping_path.encode() + profile_bytes)
        rows = kit.strict_json(profile_bytes)
        require(type(rows) is list and len(rows) == 1, 'one profile required')
        self.profile = rows[0]
        require(kit.digest(kit.canonical(self.profile)) == self.manifest['profile']['mapping_sha256'], 'packed Mapping hash mismatch')
        self.mapping_state = {**self.profile, 'comment': None, 'additional_ref': None,
                              'element': sorted(self.profile['element'], key=lambda row: (row['key'], row['value'])),
                              'metadata': [{'key': 'source_pack', 'value': kit.ID}]}
        self.mapping_state_sha = canonical_hash(self.mapping_state)
        self.artifact = {'artifact_type': 'mapping', 'identity': {'namespace': 'webhook_profiles', 'name': kit.PROFILE}}
        self.row_key = 'mapping:' + canonical_hash(self.artifact['identity'])
        self.zip_bytes = kit.read(self.directory / 'correlation-rules.zip')
        with zipfile.ZipFile(io.BytesIO(self.zip_bytes)) as archive:
            require(archive.namelist() == [kit.RULE + '.yml'], 'one exact rule required')
            member = archive.infolist()[0]
            require(member.compress_type == zipfile.ZIP_STORED and member.file_size <= HTTP_LIMIT, 'invalid rule ZIP')
            self.rule = yaml.safe_load(archive.read(member))
        require(kit.digest(kit.canonical(self.rule)) == self.manifest['rule']['authored_sha256'], 'authored rule hash mismatch')


def _fsync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _write(path, raw):
    require(len(raw) <= kit.MAX_BYTES, 'evidence file bound')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(raw)
        stream.flush()
        os.fsync(stream.fileno())
    _fsync_directory(path.parent)


class Journal:
    def __init__(self, directory, token):
        self.directory, self.token, self.sequence, self.bytes = Path(directory), token, 0, 0
        self.directory.mkdir(mode=0o700)
        _fsync_directory(self.directory.parent)

    def redact(self, value):
        if isinstance(value, dict):
            return {key.replace(self.token, '[redacted]'): '[redacted]' if any(word in key.lower() for word in ('password', 'secret', 'token', 'authorization', 'credential', 'api_key'))
                    else self.redact(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.redact(item) for item in value]
        return value.replace(self.token, '[redacted]') if isinstance(value, str) else value

    def save(self, name, value):
        raw = kit.canonical(self.redact(value))
        require(self.bytes + len(raw) <= 32 * 1024 * 1024, 'evidence aggregate bound')
        _write(self.directory / name, raw)
        self.bytes += len(raw)

    def before(self, operation, request):
        self.sequence += 1
        require(self.sequence <= 160, 'operation budget exhausted')
        name = f'{self.sequence:04d}'
        self.save(name + '-before.json', {'operation': operation, 'responsibility': 'UNKNOWN-until-recorded-response', 'request': request})
        return name

    def after(self, name, response):
        self.save(name + '-after.json', response)


class API:
    def __init__(self, target, token, journal, *, ca_file=None, transport=None):
        self.target, self.journal = target, journal
        self.deadline = time.monotonic() + DEADLINE_SECONDS
        context = ssl.create_default_context(cafile=ca_file)
        self.client = httpx.Client(headers={'X-API-Key': token}, verify=context,
                                   transport=transport or httpx.HTTPTransport(verify=context, retries=0),
                                   follow_redirects=False, trust_env=False)

    def remaining(self):
        remaining = self.deadline - time.monotonic()
        require(remaining > 0, 'installation deadline exhausted')
        return remaining

    def request(self, method, path, *, body=None, files=None):
        require(path.startswith('/api/v1/') and not path.startswith('//') and '#' not in path, 'invalid fixed API path')
        record = {'method': method, 'path': path}
        if body is not None:
            record['body'] = body
        if files is not None:
            record['file_sha256'] = kit.digest(files)
            record['file_bytes'] = len(files)
        sequence = self.journal.before('http', record) if self.journal else None
        kwargs = {'json': body} if body is not None else {}
        if files is not None:
            kwargs = {'files': {'file': ('correlation-rules.zip', files, 'application/zip')}}
        with self.client.stream(method, self.target + path, timeout=min(60, self.remaining()), **kwargs) as response:
            data = bytearray()
            for chunk in response.iter_bytes():
                require(len(data) + len(chunk) <= HTTP_LIMIT, 'HTTP response bound')
                data.extend(chunk)
                self.remaining()
            raw = bytes(data)
            if not 200 <= response.status_code < 300:
                if self.journal:
                    self.journal.after(sequence, {'status': response.status_code, 'body_sha256': kit.digest(raw), 'body_bytes': len(raw)})
                raise InstallError('HTTP status ' + str(response.status_code))
            value = kit.strict_json(raw)
            require(type(value) is dict, 'JSON object response required')
            if self.journal:
                self.journal.after(sequence, {'status': response.status_code, 'body_sha256': kit.digest(raw),
                                              'body_bytes': len(raw), 'body': value})
            return value


def _project_install(command, archive, sha, token, remaining):
    """Invoke existing target CLI once, bounded output and joined local child.

    Killing a timed-out local CLI does not prove remote rollback; attempt stays UNKNOWN.
    """
    command = Path(command)
    require(command.is_absolute() and command.name == 'oscar' and command.is_file() and not command.is_symlink(), 'explicit real target oscar executable required')
    args = [str(command), 'project', 'install', str(archive.resolve()), '--sha256', sha, '--json']
    process = subprocess.Popen(args, cwd=command.parent, env={**os.environ, 'OSCAR_CLI_TOKEN': token},
                               stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    selected = selectors.DefaultSelector()
    selected.register(process.stdout, selectors.EVENT_READ, 'stdout')
    selected.register(process.stderr, selectors.EVENT_READ, 'stderr')
    output = {'stdout': bytearray(), 'stderr': bytearray()}
    end = time.monotonic() + min(120, remaining)
    try:
        while selected.get_map():
            require(time.monotonic() < end, 'local installer outcome unknown: deadline')
            for key, _ in selected.select(min(0.1, max(0, end - time.monotonic()))):
                data = os.read(key.fileobj.fileno(), 65_536)
                if not data:
                    selected.unregister(key.fileobj)
                else:
                    output[key.data].extend(data)
                    require(sum(map(len, output.values())) <= HTTP_LIMIT, 'local installer output bound')
        require(process.wait(timeout=max(0.001, end - time.monotonic())) == 0, 'local installer outcome unknown: nonzero exit')
        return kit.strict_json(bytes(output['stdout']))
    finally:
        selected.close()
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
        process.wait()
        process.stdout.close()
        process.stderr.close()


def _runtime(api, rule_id=None):
    value = api.request('GET', RULES + '/runtime' + (f'?rule_id={rule_id}' if rule_id else ''))
    require(value.get('authoritative') is False and value.get('dispatch_enabled') is False, 'audit-only runtime required')
    dependencies = value.get('dependencies')
    require(type(dependencies) is dict and dependencies.get('status') == 'ready'
            and all(dependencies.get(name) is True for name in ('nats', 'consumer', 'redis', 'lua_registered', 'timer_store')), 'ordinary runtime dependencies unavailable')
    require(value.get('load_status') == 'loaded' and type(value.get('loaded_at')) is str and value['loaded_at']
            and positive(value.get('last_attempt_ms')) and positive(value.get('checked_at_ms')), 'runtime load evidence missing')
    expected_ids = [rule_id] if rule_id else []
    require(value.get('loaded_rule_ids') == expected_ids and type(value.get('loaded_total')) is int
            and value['loaded_total'] == len(expected_ids)
            and all(type(value.get(key)) is int and value[key] == 0
                    for key in ('loaded_omitted', 'rejected_total', 'rejected_omitted'))
            and value.get('rejected_rules') == [],
            'runtime rule inventory is incomplete or foreign')
    expected = {'id': rule_id, 'loaded': True, 'rejection': None} if rule_id else None
    require(value.get('selected_rule') == expected, 'selected rule not loaded')
    return {key: value[key] for key in ('loaded_at', 'last_attempt_ms', 'loaded_rule_ids', 'authoritative', 'dispatch_enabled')}


def _rules(api, expected=None):
    value = api.request('GET', RULES + '?page=1&perPage=100&column=id&order=asc')
    require(type(value.get('total')) is int and type(value.get('page')) is int and type(value.get('perPage')) is int
            and value['page'] == 1 and value['perPage'] == 100 and type(value.get('rows')) is list
            and value['total'] == len(value['rows']), 'incomplete rule inventory')
    if expected is None:
        require(value['rows'] == [], 'fresh exclusive target must have no correlation rules')
        return None
    require(len(value['rows']) == 1 and value['rows'][0].get('name') == kit.RULE
            and positive(value['rows'][0].get('id')), 'exact created rule identity unavailable')
    row = value['rows'][0]
    require({key: row.get(key) for key in expected} == expected, 'created rule inventory differs')
    return row['id']


def _pack_presence(api, bundle, *, installed):
    value = api.request('GET', '/api/v1/packs')
    rows = value.get('packs')
    require(type(rows) is list and len(rows) <= 256 and all(type(row) is dict for row in rows), 'bounded installed pack inventory required')
    matches = [row for row in rows if row.get('pack_id') == kit.ID]
    if not installed:
        require(not matches, 'same-name installed pack is foreign')
        return
    require(len(matches) == 1 and matches[0].get('pack_version') == kit.VERSION
            and matches[0].get('manifest_status') == 'valid' and matches[0].get('pack_content_hash') == bundle.content_hash,
            'installed pack bytes differ')


def _preview(api, bundle):
    value = api.request('POST', PREFIX + '/configuration/preview')
    require(value.get('pack_id') == kit.ID and value.get('pack_version') == kit.VERSION
            and value.get('pack_content_hash') == bundle.content_hash and value.get('counts') == {'rule': 0, 'mapping': 1, 'role': 0, 'dashboard': 0}
            and type(value.get('items')) is list and len(value['items']) == 1, 'pack preview differs')
    row = value['items'][0]
    require(all(row.get(key) == val for key, val in bundle.artifact.items()) and row.get('row_key') == bundle.row_key
            and row.get('source_file') == bundle.mapping_path and row.get('source_ordinal') == 0
            and row.get('action') == 'create' and row.get('state') == 'new' and row.get('live_hash') is None
            and row.get('expected_live_hash') is None and row.get('pack_hash') == bundle.mapping_state_sha,
            'profile conflict or unsupported preview')


def _load_status(api, bundle, load_id, *, poll):
    path = PREFIX + '/configuration/loads/' + _uuid(load_id) + '?page=1&per_page=100'
    for _ in range(120 if poll else 1):
        value = api.request('GET', path)
        require(value.get('load_id') == load_id and value.get('pack_id') == kit.ID, 'foreign load response')
        if value.get('status') in ('queued', 'running') or value.get('activation_status') in ('pending', 'running'):
            require(poll, 'original completed load is no longer complete')
            time.sleep(min(1, api.remaining()))
            continue
        require(value.get('status') == 'completed' and value.get('activation_status') == 'succeeded'
                and value.get('activation_errors') == [], 'configuration load/activation incomplete')
        for key, expected in {'created_count': 1, 'replaced_count': 0, 'skipped_count': 0, 'failed_count': 0,
                              'page': 1, 'per_page': 100, 'total': 1}.items():
            require(type(value.get(key)) is int and value[key] == expected, 'partial configuration load')
        require(value.get('activation_targets') == [{'artifact_type': 'mapping', 'namespace': 'webhook_profiles'}]
                and type(value.get('items')) is list and len(value['items']) == 1, 'unexpected activation or load items')
        item = value['items'][0]
        require(all(item.get(key) == val for key, val in bundle.artifact.items())
                and item.get('source_file') == bundle.mapping_path and item.get('source_ordinal') == 0
                and item.get('intended_action') == 'create' and item.get('result') == 'created'
                and item.get('applied_hash') == bundle.mapping_state_sha and item.get('error_code') is None,
                'load did not create the exact profile')
        return value
    raise InstallError('configuration load deadline incomplete')


def _comparison(api, bundle):
    value = api.request('GET', PREFIX + '/configuration/items/' + bundle.row_key)
    require(value.get('row_key') == bundle.row_key and all(value.get(key) == val for key, val in bundle.artifact.items())
            and value.get('state') == 'up_to_date' and value.get('changed_fields') == []
            and value.get('pack') == bundle.mapping_state and value.get('live') == bundle.mapping_state
            and all(value.get(key) == bundle.mapping_state_sha for key in ('pack_hash', 'live_hash', 'applied_hash')),
            'installed Mapping readback differs')


def _rule_readback(api, bundle, rule_id):
    require(positive(rule_id), 'original rule ID missing')
    value = api.request('GET', RULES + '/' + str(rule_id))
    require(value.get('id') == rule_id and set(bundle.rule) <= set(value)
            and kit.canonical({key: value[key] for key in bundle.rule}) == kit.canonical(bundle.rule), 'full authored rule readback differs')


def install(directory, expected_sha, target, token, command, ownership, evidence, *, ca_file=None,
            allow_owned_http=False, transport=None):
    journal = api = None
    try:
        target = origin(target, allow_owned_http)
        ownership_check(ownership, target)
        require(type(token) is str and 0 < len(token) <= 4096 and not any(c.isspace() for c in token), 'credential required')
        bundle = Bundle(directory, expected_sha)
        journal = Journal(evidence, token)
        journal.save('attempt.json', {'format': 'corrtest-install-attempt-v1', 'attempt_id': str(uuid4()),
            'target': target, 'manifest_sha256': expected_sha, 'ownership_declaration': ownership,
            'ownership_observed': False, 'limits': LIMITS, 'initial_responsibility': 'UNKNOWN-until-completed'})
        saved = journal.directory / 'kit'
        saved.mkdir(mode=0o700)
        for name in sorted(kit.ARTIFACTS + ['manifest.json', 'manifest.sha256']):
            _write(saved / name, kit.read(Path(directory) / name))
        _fsync_directory(journal.directory)
        bundle = Bundle(saved, expected_sha)
        api = API(target, token, journal, ca_file=ca_file, transport=transport)
        _pack_presence(api, bundle, installed=False)
        _rules(api)
        _runtime(api)
        validation = api.request('POST', RULES + '/validate', body=bundle.rule)
        require(validation.get('valid') is True and validation.get('errors') == [], 'target rejects authored rule')
        before = journal.before('project-install', {'command': str(command), 'archive': kit.ARCHIVE, 'archive_sha256': kit.digest(bundle.archive)})
        receipt = _project_install(command, saved / kit.ARCHIVE, kit.digest(bundle.archive), token, api.remaining())
        journal.after(before, {'receipt': receipt})
        _project_receipt(bundle, receipt)
        _pack_presence(api, bundle, installed=True)
        _preview(api, bundle)
        accepted = api.request('POST', PREFIX + '/configuration/loads', body={'pack_content_hash': bundle.content_hash,
            'overwrite': [], 'preconditions': [{'artifact': bundle.artifact, 'expected_live_hash': None}]})
        load_id = _uuid(accepted.get('load_id'))
        status_path = PREFIX + '/configuration/loads/' + load_id
        require(accepted.get('status') == 'queued' and accepted.get('status_url') in (status_path, target + status_path), 'foreign or malformed status URL')
        _load_status(api, bundle, load_id, poll=True)
        _comparison(api, bundle)
        _rules(api)
        _runtime(api)
        imported = api.request('POST', RULES + '/import', files=bundle.zip_bytes)
        require(imported == {'created': [kit.RULE], 'updated': [], 'errors': []}, 'partial or non-created rule import')
        rule_id = _rules(api, bundle.rule)
        _rule_readback(api, bundle, rule_id)
        runtime = _runtime(api, rule_id)
        _pack_presence(api, bundle, installed=True)
        _comparison(api, bundle)
        _, journal_sha = _journal_image(journal.directory, journal.sequence, completed=False)
        completed = {'journal_operations': journal.sequence, 'journal_sha256': journal_sha,
            'format': 'corrtest-install-completed-v1', 'status': 'completed', 'target': target,
            'manifest_sha256': expected_sha, 'pack_content_hash': bundle.content_hash,
            'project_operation_id': receipt['operation_id'], 'load_id': load_id, 'rule_id': rule_id,
            'profile_effective_sha256': bundle.manifest['profile']['effective_sha256'],
            'profile_revision_observed': False,
            'authored_rule_sha256': bundle.manifest['rule']['authored_sha256'], 'runtime_binding': runtime,
            'ownership_declaration': ownership, 'ownership_observed': False, 'limits': LIMITS,
            'scope': 'installation-readback-only-not-scenario-PASS'}
        journal.save('completed.json', completed)
        return completed
    except Exception as error:
        if journal:
            try:
                journal.save('failed.json', {'status': 'INCONCLUSIVE', 'error_type': type(error).__name__,
                    'responsibility': 'retain-attempt-no-retry-no-rollback', 'last_sequence': journal.sequence})
            except Exception:
                pass  # Original exclusive directory still blocks reuse, even if final recording failed.
        raise InstallError('installation incomplete; retain original evidence; ' + type(error).__name__) from None
    finally:
        if api:
            api.client.close()


def _journal_image(directory, count, *, completed):
    """Bounded local integrity check, not an authenticity or ownership proof."""
    require(positive(count) and count <= 160, 'original journal count required')
    names = {'attempt.json', 'kit'} | ({'completed.json'} if completed else set())
    records, hashes, size = [], [], 0
    for number in range(1, count + 1):
        pair = []
        for kind in ('before', 'after'):
            name = f'{number:04d}-{kind}.json'
            names.add(name)
            path = directory / name
            try:
                require(stat.S_IMODE(path.lstat().st_mode) == 0o600, 'original private journal required')
                raw = kit.read(path)
                value = kit.strict_json(raw)
            except (OSError, ValueError) as error:
                raise InstallError('original journal is missing or invalid') from error
            size += len(raw)
            require(size <= 32 * 1024 * 1024 and type(value) is dict and kit.canonical(value) == raw,
                    'original journal bound or canonical shape differs')
            hashes.append({'path': name, 'bytes': len(raw), 'sha256': kit.digest(raw)})
            pair.append(value)
        records.append(pair)
    observed = set()
    with os.scandir(directory) as entries:
        for entry in entries:
            require(len(observed) < 323 and entry.name in names, 'unexpected attempt artifact')
            observed.add(entry.name)
    require(observed == names, 'original attempt is incomplete')
    return records, kit.digest(kit.canonical(hashes))


def _project_receipt(bundle, receipt):
    require(type(receipt) is dict and receipt.get('status') == 'complete' and receipt.get('command') == 'install'
            and receipt.get('pack_id') == kit.ID and receipt.get('old_version') is None and receipt.get('new_version') == kit.VERSION
            and receipt.get('manifest_sha256') == bundle.pack_manifest_sha, 'local install receipt differs or incomplete')
    for key in ('inserted', 'updated', 'disabled', 'purged', 'schedules_changed', 'renamed', 'preapplied', 'missing', 'quarantined'):
        require(type(receipt.get(key)) is int and receipt[key] == 0, 'unexpected project side effects')
    return _uuid(receipt.get('operation_id'))


class _JournalReadback:
    """Replay the fixed response validators over retained bytes; never sends."""
    def __init__(self, records):
        self.records = iter(records)

    def request(self, method, path):
        try:
            before, after = next(self.records)
        except StopIteration as error:
            raise InstallError('original readback missing') from error
        require(before['operation'] == 'http' and before['request'] == {'method': method, 'path': path},
                'original readback order differs')
        return after['body']


def _original_journal(evidence, completed, bundle):
    rows, fingerprint = _journal_image(evidence, completed.get('journal_operations'), completed=True)
    require(fingerprint == completed.get('journal_sha256'), 'original journal hash differs')
    load_id, rule_id = _uuid(completed.get('load_id')), completed.get('rule_id')
    require(positive(rule_id), 'original rule ID required')
    load_path = PREFIX + '/configuration/loads/' + load_id
    status_path = load_path + '?page=1&per_page=100'
    list_path = RULES + '?page=1&perPage=100&column=id&order=asc'
    final_paths = [list_path, RULES + '/' + str(rule_id), RULES + f'/runtime?rule_id={rule_id}',
                   '/api/v1/packs', PREFIX + '/configuration/items/' + bundle.row_key]
    allowed_gets = set(final_paths) | {RULES + '/runtime', status_path}
    posts = {
        RULES + '/validate': {'method': 'POST', 'path': RULES + '/validate', 'body': bundle.rule},
        PREFIX + '/configuration/preview': {'method': 'POST', 'path': PREFIX + '/configuration/preview'},
        PREFIX + '/configuration/loads': {'method': 'POST', 'path': PREFIX + '/configuration/loads',
            'body': {'pack_content_hash': bundle.content_hash, 'overwrite': [],
                     'preconditions': [{'artifact': bundle.artifact, 'expected_live_hash': None}]}},
        RULES + '/import': {'method': 'POST', 'path': RULES + '/import',
                           'file_sha256': kit.digest(bundle.zip_bytes), 'file_bytes': len(bundle.zip_bytes)},
    }
    mutations, statuses = {}, []
    for index, (before, after) in enumerate(rows):
        require(set(before) == {'operation', 'responsibility', 'request'}
                and before['responsibility'] == 'UNKNOWN-until-recorded-response' and type(before['request']) is dict,
                'original attempt record differs')
        request = before['request']
        if before['operation'] == 'project-install':
            require('project-install' not in mutations and set(after) == {'receipt'}
                    and set(request) == {'command', 'archive', 'archive_sha256'}
                    and type(request['command']) is str and Path(request['command']).is_absolute()
                    and Path(request['command']).name == 'oscar'
                    and request['archive'] == kit.ARCHIVE and request['archive_sha256'] == kit.digest(bundle.archive),
                    'original project request differs')
            require(_project_receipt(bundle, after['receipt']) == completed['project_operation_id'],
                    'original project operation differs')
            mutations['project-install'] = index
            continue
        require(before['operation'] == 'http' and set(after) == {'status', 'body_sha256', 'body_bytes', 'body'}
                and type(after['status']) is int and 200 <= after['status'] < 300 and type(after['body']) is dict
                and type(after['body_sha256']) is str and kit.DIGEST.fullmatch(after['body_sha256'])
                and positive(after['body_bytes']) and after['body_bytes'] <= HTTP_LIMIT, 'original HTTP response differs')
        path = request.get('path')
        if request.get('method') == 'GET':
            require(set(request) == {'method', 'path'} and path in allowed_gets and after['status'] == 200,
                    'unexpected original GET')
            if path == status_path:
                statuses.append((index, (before, after)))
            continue
        require(path in posts and path not in mutations and kit.canonical(request) == kit.canonical(posts[path]),
                'unexpected or repeated original mutation')
        mutations[path] = index
        body = after['body']
        if path.endswith('/validate'):
            require(body.get('valid') is True and body.get('errors') == [], 'original validation failed')
        elif path.endswith('/loads'):
            require(after['status'] == 202 and body.get('load_id') == load_id and body.get('status') == 'queued'
                    and body.get('status_url') in (load_path, completed['target'] + load_path), 'original accepted load differs')
        elif path.endswith('/import'):
            require(body == {'created': [kit.RULE], 'updated': [], 'errors': []}, 'original import was not created-only')
    phases = [RULES + '/validate', 'project-install', PREFIX + '/configuration/preview',
              PREFIX + '/configuration/loads', RULES + '/import']
    require(set(mutations) == set(phases) and [mutations[key] for key in phases] == sorted(mutations.values()),
            'original mutation sequence differs')
    require(statuses and all(mutations[phases[3]] < index < mutations[phases[4]] for index, _ in statuses),
            'original load was not observed before import')
    _load_status(_JournalReadback([statuses[-1][1]]), bundle, load_id, poll=False)
    require(len(rows) >= 5 and mutations[phases[4]] < len(rows) - 5, 'original final readback missing')
    original = _JournalReadback(rows[-5:])
    require(_rules(original, bundle.rule) == rule_id, 'original rule identity differs')
    _rule_readback(original, bundle, rule_id)
    require(_runtime(original, rule_id) == completed['runtime_binding'], 'original runtime binding differs')
    _pack_presence(original, bundle, installed=True)
    _comparison(original, bundle)


def verify_install(evidence, expected_sha, target, token, ownership, *, ca_file=None, allow_owned_http=False, transport=None):
    target = origin(target, allow_owned_http)
    ownership_check(ownership, target)
    evidence = Path(evidence)
    require(not evidence.is_symlink() and stat.S_IMODE(evidence.stat().st_mode) == 0o700, 'original private attempt required')
    require(not (evidence / 'failed.json').exists(), 'failed attempt cannot be verified as completed')
    completed = _private_json(evidence / 'completed.json')
    attempt = _private_json(evidence / 'attempt.json')
    require(type(completed) is dict and set(completed) == COMPLETED_FIELDS
            and completed.get('ownership_observed') is False and completed.get('profile_revision_observed') is False
            and completed.get('limits') == LIMITS and completed.get('scope') == 'installation-readback-only-not-scenario-PASS',
            'original completion contract differs')
    _uuid(completed.get('project_operation_id'))
    require(completed.get('format') == 'corrtest-install-completed-v1' and completed.get('status') == 'completed'
            and completed.get('target') == target and completed.get('manifest_sha256') == expected_sha
            and attempt.get('target') == target and attempt.get('manifest_sha256') == expected_sha
            and completed.get('ownership_declaration') == ownership and attempt.get('ownership_declaration') == ownership,
            'original completed installation binding differs')
    bundle = Bundle(evidence / 'kit', expected_sha)
    require(completed.get('pack_content_hash') == bundle.content_hash
            and completed.get('profile_effective_sha256') == bundle.manifest['profile']['effective_sha256']
            and completed.get('authored_rule_sha256') == bundle.manifest['rule']['authored_sha256'], 'completed artifact binding differs')
    _original_journal(evidence, completed, bundle)
    api = API(target, token, None, ca_file=ca_file, transport=transport)
    try:
        _pack_presence(api, bundle, installed=True)
        _load_status(api, bundle, completed['load_id'], poll=False)
        _comparison(api, bundle)
        require(_rules(api, bundle.rule) == completed['rule_id'], 'original rule identity changed')
        _rule_readback(api, bundle, completed['rule_id'])
        require(_runtime(api, completed['rule_id']) == completed['runtime_binding'], 'original runtime load binding changed')
        return {'status': 'current-readback-matches', 'manifest_sha256': expected_sha, 'target': target,
                'ownership_observed': False, 'limits': LIMITS, 'scope': completed['scope']}
    finally:
        api.client.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--kit', type=Path)
    parser.add_argument('--manifest-sha256', required=True)
    parser.add_argument('--target', required=True)
    parser.add_argument('--token-file', required=True, type=Path)
    parser.add_argument('--ownership-file', required=True, type=Path)
    parser.add_argument('--oscar-command', type=Path)
    parser.add_argument('--ca-file')
    parser.add_argument('--allow-owned-http', action='store_true')
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--evidence', type=Path)
    mode.add_argument('--verify', type=Path)
    args = parser.parse_args()
    try:
        require(stat.S_IMODE(args.token_file.lstat().st_mode) == 0o600, 'token file must be private')
        token = kit.read(args.token_file).decode('utf-8').strip()
        ownership = kit.strict_json(kit.read(args.ownership_file))
        options = {'ca_file': args.ca_file, 'allow_owned_http': args.allow_owned_http}
        if args.verify:
            result = verify_install(args.verify, args.manifest_sha256, args.target, token, ownership, **options)
        else:
            require(args.kit is not None and args.oscar_command is not None, 'kit and target oscar command required')
            result = install(args.kit, args.manifest_sha256, args.target, token, args.oscar_command, ownership, args.evidence, **options)
        print(json.dumps(result, sort_keys=True).replace(token, '[redacted]'))
        return 0
    except Exception as error:
        print(json.dumps({'status': 'INCONCLUSIVE', 'error_type': type(error).__name__, 'message': 'retain attempt; no automatic retry or rollback'}))
        return 4


if __name__ == '__main__':
    raise SystemExit(main())
