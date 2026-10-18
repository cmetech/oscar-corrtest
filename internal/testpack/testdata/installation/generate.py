"""Regenerate host journal using the real installer and its existing controlled peer.

No network/process/target authority: only the installer API transport and existing
project command boundary are controlled. The production installer emits all files.
"""
import hashlib
import importlib
import json
from pathlib import Path
import shutil
import sys
import tempfile

import httpx

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / 'test-packs'))
kit = importlib.import_module("build_operational")
installer = importlib.import_module("install_operational")
controls = importlib.import_module("test_install_operational")

HERE = Path(__file__).resolve().parent
DISTRIBUTION = HERE.parent / 'kit'
ownership = {'format': 'corrtest-owned-target-v1', 'target': controls.ORIGIN,
             'owner_reference': 'root-owned-target-1', 'exclusive_through_export': True,
             'stable_configuration_and_processes': True, 'destinations_history_or_controlled_lab': True}
with tempfile.TemporaryDirectory() as temporary:
    evidence = Path(temporary) / 'attempt'
    peer = controls.Peer(installer, DISTRIBUTION, evidence)
    installer._project_install = peer.tool
    installer.time.sleep = lambda _: None  # deterministic bounded poll scheduling only
    polls = 0

    def respond(request):
        global polls
        response = peer.handle(request)
        value = response.json()
        if '/configuration/loads/' in request.url.path:
            polls += 1
            if polls < 3:
                value = {'load_id': controls.LOAD_ID, 'pack_id': kit.ID,
                         'status': 'queued' if polls == 1 else 'running', 'activation_status': 'pending'}
        # Deliberately retain a real noncanonical wire serialization. The journal
        # stores its hash, but stores only the parsed body, not these raw bytes.
        return httpx.Response(response.status_code, content=json.dumps(value, indent=2).encode())

    completed = installer.install(DISTRIBUTION, kit.digest((DISTRIBUTION / 'manifest.json').read_bytes()),
        controls.ORIGIN, controls.TOKEN, Path('/owned/oscar'), ownership, evidence,
        transport=httpx.MockTransport(respond))
    peer.calls.clear()
    verified = installer.verify_install(evidence, completed['manifest_sha256'], controls.ORIGIN,
        controls.TOKEN, ownership, transport=httpx.MockTransport(respond))
    assert verified['status'] == 'current-readback-matches'
    assert peer.calls and all(method == 'GET' for method, _ in peer.calls)
    destination = HERE / 'records'
    destination.mkdir(exist_ok=True)
    for old in destination.iterdir():
        if old.is_file():
            old.unlink()
    for source in sorted(evidence.glob('*.json')):
        assert controls.TOKEN.encode() not in source.read_bytes()
        shutil.copyfile(source, destination / source.name)
    provenance = {'scope': 'actual Python installer with controlled HTTP/project-command boundaries; not deployed proof',
                  'manifest_sha256': completed['manifest_sha256'], 'operations': completed['journal_operations'],
                  'producer_sources': {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
                     for p in (ROOT / 'test-packs' / name for name in
                        ('build_operational.py', 'install_operational.py', 'test_install_operational.py'))},
                  'files': {p.name: {'bytes': len(p.read_bytes()), 'sha256': hashlib.sha256(p.read_bytes()).hexdigest()}
                            for p in sorted(destination.iterdir())},
                  'python_verification': verified}
    (HERE / 'provenance.json').write_bytes(kit.canonical(provenance))
    print(json.dumps({'operations': completed['journal_operations'], 'files': len(provenance['files']),
                      'verification': verified['status']}))
