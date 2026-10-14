"""Run unchanged CorrTest ownership tests on an isolated Linux kernel."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
from uuid import uuid4


IMAGE = 'sha256:9ea93ed22de014e1d4c39321d95fa514b038323631bb91fc5dbce53eeff0291d'
GO = Path.home() / 'go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.darwin-arm64/bin/go'
ROOT = Path(__file__).resolve().parents[4]
OUT = Path(tempfile.mkdtemp(prefix='corrtest-linux-ownership-evidence-'))
LABEL = 'oscar.corrtest-qualification'
OWNER = uuid4().hex
NAME = 'corrtest-linux-ownership-' + OWNER[:12]


def command(args, name, *, env=None, timeout=120):
    result = subprocess.run(args, cwd=ROOT, env=env, capture_output=True, timeout=timeout)
    output = result.stdout + result.stderr
    (OUT / (name + '.log')).write_bytes(output)
    record = dict(command=[str(arg) for arg in args], exit=result.returncode,
        log=name + '.log', sha256=hashlib.sha256(output).hexdigest())
    records.append(record)
    if result.returncode:
        raise RuntimeError(name + ' failed; evidence: ' + str(OUT))
    return result.stdout


def sources():
    paths = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).decode().split('\0')
    return {name: hashlib.sha256((ROOT / name).read_bytes()).hexdigest()
        for name in paths if name.endswith(('.go', '/go.mod', '/go.sum')) or name in ('go.mod', 'go.sum')}


records, before, cleanup = [], sources(), {}
created = False
try:
    metadata = [json.loads(item) for item in command(['docker', 'image', 'inspect', '--format',
        '{{json .Id}}|{{json .Os}}|{{json .Architecture}}|{{json (index .Config "Volumes")}}', IMAGE], 'image-inspect').decode().strip().split('|')]
    assert metadata[:3] == [IMAGE, 'linux', 'arm64']
    assert not metadata[3], 'image would create anonymous volumes'
    command([str(GO), 'version'], 'compiler')
    with tempfile.TemporaryDirectory(prefix='corrtest-linux-ownership-binaries-') as binary_directory:
        binary = Path(binary_directory)
        binary.chmod(0o755)
        environment = {**os.environ, 'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': 'arm64',
            'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off'}
        for package in ('runtime', 'integration'):
            command([str(GO), 'test', '-c', '-o', str(binary / (package + '.test')), './internal/' + package],
                'compile-' + package, env=environment)
        binary_hashes = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in binary.iterdir()}
        args = ['docker', 'create', '--name', NAME, '--label', LABEL + '=' + OWNER,
            '--network', 'none', '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges',
            '--pids-limit', '64', '--memory', '512m', '--cpus', '2', '--user', '65534:65534',
            '--tmpfs', '/tmp:rw,nosuid,nodev,size=256m,mode=1777',
            '--mount', 'type=bind,src=' + str(binary) + ',dst=/proof,readonly',
            '--entrypoint', '/bin/sh', IMAGE, '-c',
            "uname -a && /proof/runtime.test -test.v -test.timeout=45s -test.run='TestSecondRuntimeCannotInterruptActiveRun|TestRuntimeState' && "
            "/proof/integration.test -test.v -test.timeout=45s -test.run='^TestStatusCLICannotRecoverLiveUIRuntime$'"]
        command(args, 'container-create')
        created = True
        try:
            command(['docker', 'start', '--attach', NAME], 'linux-tests')
            info = json.loads(command(['docker', 'inspect', '--format',
                '{"labels":{{json .Config.Labels}},"state":{{json .State}},"network":{{json .HostConfig.NetworkMode}},"mounts":{{json .Mounts}}}',
                NAME], 'container-inspect'))
            assert info['labels'][LABEL] == OWNER
            assert info['state']['Status'] == 'exited' and info['state']['ExitCode'] == 0
            assert info['network'] == 'none'
            assert not [mount for mount in info['mounts'] if mount['Type'] == 'volume']
        finally:
            if created:
                owner = subprocess.run(['docker', 'inspect', '--format', '{{index .Config.Labels "' + LABEL + '"}}', NAME],
                    capture_output=True, text=True, timeout=20)
                assert owner.returncode == 0 and owner.stdout.strip() == OWNER, 'cleanup owner mismatch'
                command(['docker', 'rm', '-f', '-v', NAME], 'container-remove')
                gone = subprocess.run(['docker', 'inspect', NAME], capture_output=True, timeout=20)
                (OUT / 'container-absent.log').write_bytes(gone.stdout + gone.stderr)
                assert gone.returncode and b'no such' in gone.stderr.lower(), 'container remained after removal'
                cleanup = dict(container=NAME, owner_label_verified=True, container_absent=True,
                    named_networks_created=0, volumes_created=0, images_built=0, images_pulled=0)
        cleanup['temporary_binaries_removed'] = False
    cleanup['temporary_binaries_removed'] = not binary.exists()
finally:
    after = sources()
    manifest = dict(base=subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip(),
        image=IMAGE, records=records, source_before=before, source_after=after,
        source_unchanged=before == after, binaries=locals().get('binary_hashes', {}), cleanup=cleanup,
        limits=['Linux ARM64 Docker VM and local tmpfs only', 'No Windows or power-loss qualification',
            'No live OSCAR endpoint, source injection, parent/History commissioning or UAT acceptance'])
    (OUT / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(OUT, flush=True)
    assert before == after, 'source changed during qualification'
