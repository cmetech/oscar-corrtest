# Install and run oscar-corrtest

`oscar-corrtest` is one self-contained executable. Installation is user-scoped,
does not require `sudo`, and does not create or start a service. The UI listens
on `0.0.0.0:8787` by default so it can be opened directly from another machine.

No credential is required to install the application, start the UI, browse
saved reports, or preview scenarios. An OSCAR external API key is required only
when doctor or a test run contacts an OSCAR target. TLS certificates, a UI
token, a reverse proxy, and an SSH tunnel are optional hardening choices—not
startup requirements.

The anonymous installers work after the public
`cmetech/oscar-corrtest` repository has at least one GitHub Release.

## Linux and macOS

Install the latest release:

```sh
curl -fsSL https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.sh | sh
```

The default destination is `$HOME/.local/bin/oscar-corrtest`. The installer
downloads the release archive and `SHA256SUMS`, verifies the selected archive,
rejects unsafe archive members, and atomically replaces only the executable.
It never starts `serve` or writes configuration, SQLite, evidence, or
credentials.

Pin a release or choose another absolute user-owned destination:

```sh
curl -fsSL https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.sh |
  OSCAR_CORRTEST_VERSION=v1.2.3 \
  OSCAR_CORRTEST_INSTALL_DIR="$HOME/bin" sh
```

For an internal release mirror, set `OSCAR_CORRTEST_RELEASE_BASE_URL`. To
override latest-version discovery, set `OSCAR_CORRTEST_RELEASE_API_URL`.

If `$HOME/.local/bin` is not already in `PATH`, add it to the shell profile and
open a new shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

## Windows

From PowerShell, install the latest Windows amd64 release:

```powershell
irm https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.ps1 | iex
```

The default destination is
`%LOCALAPPDATA%\oscar-corrtest\bin\oscar-corrtest.exe`. The installer verifies
SHA-256 before replacing the executable and adds that directory to the current
user's `PATH` when needed. It does not change machine-wide configuration or
install/start a Windows service.

Pin a release or destination before running the same command:

```powershell
$env:OSCAR_CORRTEST_VERSION = 'v1.2.3'
$env:OSCAR_CORRTEST_INSTALL_DIR = "$HOME\bin"
irm https://raw.githubusercontent.com/cmetech/oscar-corrtest/main/scripts/install.ps1 | iex
```

Windows arm64 is not part of the initial release set.

## Manual checksum installation

Download the matching archive and `SHA256SUMS` from the same GitHub Release.
The required names are:

- `oscar-corrtest_vX.Y.Z_linux_amd64.tar.gz`
- `oscar-corrtest_vX.Y.Z_linux_arm64.tar.gz`
- `oscar-corrtest_vX.Y.Z_darwin_amd64.tar.gz`
- `oscar-corrtest_vX.Y.Z_darwin_arm64.tar.gz`
- `oscar-corrtest_vX.Y.Z_windows_amd64.zip`

On Linux, verify with `sha256sum -c SHA256SUMS`. On macOS, use
`shasum -a 256 -c SHA256SUMS`. On Windows, compare
`Get-FileHash -Algorithm SHA256 <archive>` with the exact row in
`SHA256SUMS`. Extract the archive into a private temporary directory, then copy
only `oscar-corrtest/bin/oscar-corrtest` (or `.exe`) into a user-owned binary
directory.

## First start and OSCAR target

Start the UI explicitly:

```sh
oscar-corrtest serve
```

Then open `http://<server-ip>:8787`. The default server is HTTP and has no UI
authentication. Anyone who can reach the port can view evidence, create
temporary rules, and inject alerts through a configured OSCAR target, so use a
test-network firewall boundary appropriate for the lab.

Set the OSCAR external API key only when adding or using a target:

```sh
export OSCAR_API_KEY='<your-api-key>'
oscar-corrtest target add \
  --name lab-a \
  --url https://oscar.example/ext/mw \
  --credential-env OSCAR_API_KEY
oscar-corrtest target list
oscar-corrtest doctor --target <target-id> --pipeline-mode phase_b_dispatch
```

Only the environment-variable name is stored. For persistence across shells,
put the key in a protected regular file and use
`--credential-file /absolute/path/to/api-key` instead. A custom `--ca-file` is
needed only when OSCAR uses a private certificate authority. `--insecure` is a
diagnostic escape hatch and should not be normal configuration.

On PowerShell, the equivalent API-key assignment is:

```powershell
$env:OSCAR_API_KEY = '<your-api-key>'
oscar-corrtest.exe target add --name lab-a --url https://oscar.example/ext/mw --credential-env OSCAR_API_KEY
```

## Listener and optional UI hardening

Use loopback explicitly when only local browser access is desired:

```sh
oscar-corrtest serve --listen 127.0.0.1:8787
```

Optional direct bearer authentication requires both a separate UI token and
TLS. This token is not the OSCAR API key:

```sh
oscar-corrtest serve --remote-mode bearer \
  --auth-token-file /absolute/path/to/corrtest-ui-token \
  --tls-cert /absolute/path/to/tls.crt \
  --tls-key /absolute/path/to/tls.key
```

An authenticated reverse proxy can instead use `--remote-mode trusted-proxy`
with an exact identity header/value and trusted proxy CIDRs. See
[operator.md](operator.md) for the full policy.

## Configuration and state

Default user paths on Linux and macOS are:

- configuration: `$XDG_CONFIG_HOME/oscar-corrtest/config.json`, or
  `$HOME/.config/oscar-corrtest/config.json`;
- SQLite and evidence: `$XDG_STATE_HOME/oscar-corrtest`, or
  `$HOME/.local/state/oscar-corrtest`.

Flags override `OSCAR_CORRTEST_*` environment variables, which override the
versioned JSON configuration file, which overrides defaults. A minimal file is:

```json
{
  "apiVersion": "corrtest.oscar/v1alpha1",
  "listenAddress": "0.0.0.0:8787"
}
```

## Upgrade, backup, and uninstall

Upgrade by rerunning the installer. It verifies and atomically replaces only
the executable; existing configuration, targets, reports, SQLite, and evidence
remain in place.

While the application is running, create a coordinated SQLite snapshot:

```sh
oscar-corrtest backup --output "$HOME/corrtest-backup.db"
```

That command does not include `runs/` evidence directories. For a complete
host backup, stop `serve` and copy the entire state directory, including
`corrtest.db`, any WAL/SHM files, and `runs/`. Portable long-term results can
also be created with `oscar-corrtest export <run-id> --output evidence.zip`.

To uninstall on Linux or macOS, remove only the installed executable:

```sh
rm "$HOME/.local/bin/oscar-corrtest"
```

On Windows, remove
`%LOCALAPPDATA%\oscar-corrtest\bin\oscar-corrtest.exe` and optionally remove
that directory from the current-user `PATH`. These steps intentionally preserve
all corrtest state. Delete the state/configuration directories separately only
after backing them up and confirming their exact location.
