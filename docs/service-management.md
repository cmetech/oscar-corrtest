# User service management

CorrTest can register itself as a current-user background service. This is
optional and never requires root or Administrator access. The binary installer
does not start CorrTest and does not install the service; both actions remain
explicit.

## Install and start

```sh
oscar-corrtest service install
oscar-corrtest service start
oscar-corrtest service status
```

`service install` writes or updates the current-user definition and enables it
for future logins. It intentionally does not start the process immediately.
`service start` starts it now. The generated definition runs
`oscar-corrtest serve` with the normal user paths and no TLS or UI-authentication
flags. The OSCAR API key remains in the managed `.env`; it is never embedded in
the definition.

Platform mechanisms and definitions are:

- Linux: systemd user unit `~/.config/systemd/user/oscar-corrtest.service`;
- macOS: LaunchAgent `~/Library/LaunchAgents/io.cmetech.oscar-corrtest.plist`;
- Windows: current-user Task Scheduler entry `OSCAR CorrTest`, with its generated
  XML under `%LOCALAPPDATA%\oscar-corrtest`.

## Operate and diagnose

```sh
oscar-corrtest service status
oscar-corrtest service restart
oscar-corrtest service stop
oscar-corrtest service logs
oscar-corrtest service logs --lines 500 --no-follow
```

Status exits 0 only while running, 3 when stopped or not installed, and 1 for
an operational error. Logs prints the latest 200 `application.jsonl` records
and follows by default. The Operations page provides the same status, stop and
restart controls while the UI is reachable.

## Uninstall the service

```sh
oscar-corrtest service uninstall
```

Uninstall stops the managed process and removes only its current-user service
definition. It preserves the executable, JSON configuration, managed `.env`,
SQLite database, evidence, reports, and logs. Remove those separately only
after confirming their displayed paths and taking any required backup.
