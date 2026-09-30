# Development

Install the project toolchain with `mise install`, then run:

```sh
mise run fmt
mise run check
mise run build
./bin/flatpak-dev --help
```

`check` verifies formatting and runs `go vet` and `go test`. `build` disables cgo and writes `bin/flatpak-dev`. To apply Go's source fixes, run `mise exec -- go fix ./...` before formatting.

## Releases

Run the **Release** workflow from GitHub Actions and enter a version such as `0.1.0`, without a `v` prefix. It installs the tools declared in `mise.toml`, runs `mise run check`, and calls `mise run build` for Linux x64 and ARM64. The workflow archives the binaries and uploads them to a GitHub release. The release tag points to the checked-out commit. Existing tags are rejected.

The workflow sets `GOOS`, `GOARCH`, and `FLATPAK_DEV_VERSION` for each build. Regular builds report `dev` through `--version`; release builds report their version.

## Code layout

| File | Responsibility |
| --- | --- |
| `cmd/flatpak-dev/main.go` | Commands, flags, signals, and error reporting |
| `internal/dev/project.go` | Project paths and manifest discovery |
| `internal/dev/build.go`, `tools.go`, `openssh.json` | Application dependencies, session environment, and separate OpenSSH build |
| `internal/dev/service.go` | Background sandbox, readiness, and idle shutdown |
| `internal/dev/session.go` | SSH client and sandbox connections |
| `internal/dev/ssh_config.go`, `templates.go`, `templates/` | Keys, SSH configuration, profiles, and shell scripts |
| `internal/dev/state.go`, `git.go` | State directories, build lock, and local Git exclusion |

The CLI uses `urfave/cli` for command parsing, `shellescape` for shell arguments, `atomicfile` for generated files, and `gofrs/flock` for file locks. Templates use Go's `text/template` and `embed`. Shell quoting, SSH config quoting, and SSH percent-token escaping have different rules and stay separate.

## Connections

The public commands are `run`, `ssh connect`, and `ssh config`. Command flags after the `run` executable belong to the sandbox command.

`run` and `ssh connect` prepare a client key and private SSH config, then replace the CLI process with the host SSH client. SSH handles the terminal, signals, forwarding, and command exit status. The installed `flatpak enter` returned success for a command that exited with code 7 during prototype checks, so commands use SSH to preserve their exit status.

Both CLI and editor connections use a generated `ProxyCommand`. It calls the hidden `_connect` command, which starts or reuses the project's sandbox and launches `sshd -i` through `flatpak enter`. Each connection gets an SSH handler inside the same sandbox. The handler communicates over stdin/stdout without a listening port.

`ssh config` writes an editor host entry without starting the sandbox. Its proxy discovers the manifest when connecting unless the user supplies `--manifest`. CLI connections use their private config without changing `~/.ssh/config`.

## Preparation and profiles

Flatpak Builder resolves JSON/YAML manifests and includes. Discovery searches the checkout root and `flatpak/`, ignoring `.dev` and `.devel` variants. Dependencies build up to the final application module, which is skipped. The checkout stays mounted for application builds and commands.

`openssh.json` builds OpenSSH separately against the project's SDK. It uses OpenSSH's standard installation target and never adds an SSH module to the application's manifest or build output. Builder caches both dependency and OpenSSH builds.

Builder's `--run` applies top-level build options. The CLI captures that environment and applies the final module's environment and path overrides. The generated session profile changes into the checkout and sources the live `flatpak/.profile`.

SSH resets the environment and Flatpak exposes `/bin/sh` as the account shell. A shell wrapper selects Bash, which loads the generated profile for commands, interactive shells, and SFTP. It dispatches the `internal-sftp` request to OpenSSH's external SFTP helper. Passing the helper as a quoted argument avoids OpenSSH reconstructing its path incorrectly when it contains spaces and apostrophes.

The CLI adds `--die-with-parent` and `--readonly` to a generated copy of the resolved manifest. Builder's `--run` accepts those flags through `build-options.build-args`. Preparation runs again when a new sandbox starts; profile edits apply to the next session.

## Lifetime and locks

A transient systemd user service runs the hidden `_serve` supervisor. It launches `flatpak-builder --run` with `sandbox.sh`, which writes the instance ID from `/.flatpak-info` and waits. The supervisor stops Builder after 30 seconds without open connections.

Each proxy holds a shared `use.lock` while it starts or uses the sandbox. A second shared lock inside the sandbox covers the SSH handler if its host proxy disappears. The kernel releases locks when their processes exit. The proxy updates the lock file's modification time on connection and disconnection so short sessions also reset the grace period.

`build.lock` serializes key generation, configuration writes, preparation, and startup. Proxies acquire the usage lock before the build lock. The supervisor only uses the usage lock. It must hold that lock exclusively until Builder exits and the instance marker is removed. Waiting for either lock respects cancellation.

Git exclusions are checked and written under the build lock. Editor configuration also uses a shared `ssh-config.lock` under `$XDG_RUNTIME_DIR/flatpak-dev/` so two projects cannot claim the same host alias concurrently. That lock is acquired before the project's build lock; startup never acquires it. `ssh config` requires an absolute `XDG_RUNTIME_DIR`, supplied by the user's login session.

The service uses `KillMode=process` so the supervisor can stop Builder while its FUSE helper finishes unmounting. Builder receives a parent-death signal if the supervisor exits unexpectedly. Flatpak's `--die-with-parent` tears down the sandbox. Killing the entire service group can interrupt the unmount and leave a stale mount.

An open SSH connection counts as use regardless of traffic. This includes editor ControlMaster connections. Detached jobs end when the sandbox stops. Traffic-based SSH timeouts could interrupt quiet commands, so shutdown depends on open connections instead.

## Editor compatibility

[Zed's SSH transport](https://github.com/zed-industries/zed/blob/main/crates/remote/src/transport/ssh.rs) uses SFTP for server binary and directory uploads. Microsoft's [Remote SSH issue with a server lacking SFTP](https://github.com/microsoft/vscode-remote-release/issues/9103) shows VS Code's `scp` fallback failing on a subsystem request. [Modern OpenSSH scp uses SFTP by default](https://man.openbsd.org/scp). OpenSSH also provides the TCP and Unix socket forwarding editors use to reach their remote helpers.

The sandbox creates VS Code's prerequisite-check marker because its [Linux check script](https://github.com/microsoft/vscode/blob/main/resources/server/bin/helpers/check-requirements-linux.sh) misses the SDK's multiarch library paths. The marker skips that probe; it does not supply missing libraries.

Protocol checks have covered shared startup, argument quoting, exit codes, profiles, terminal allocation, SFTP/SCP, forwarding, multiplexing, and idle cleanup. A connection from Zed has also been tested with Mixamp; the VS Code interface still needs testing.

## Service inspection

Find a running sandbox with:

```sh
systemctl --user list-units 'flatpak-*.service'
```

Copy its service name into:

```sh
systemctl --user status SERVICE_NAME
journalctl --user -t SERVICE_NAME
```

Add `--follow` to `journalctl` for live output. Startup errors print the journal command.

## Generated files

Project files live under `.flatpak-dev/`:

| Path | Purpose |
| --- | --- |
| `build/`, `builder-state/` | Application dependency image and Builder cache |
| `tools/`, `tools-cache/`, `tools.json` | OpenSSH build and cache |
| `run.json` | Resolved manifest with launch flags |
| `session.profile` | SDK environment and project profile loader |
| `home/` | Persistent sandbox home |
| `ssh/` | Keys, SSH configurations, and session script |
| `sandbox.sh`, `instance` | Startup script and current instance ID |
| `build.lock`, `use.lock` | Preparation and lifetime locks |

Editor entries live in `$XDG_DATA_HOME/flatpak-dev/ssh`, defaulting to `~/.local/share/flatpak-dev/ssh`. An unset, empty, or relative `XDG_DATA_HOME` uses that default. The global configuration lock lives in `$XDG_RUNTIME_DIR/flatpak-dev/ssh-config.lock`. The CLI adds its local Git exclusion to `.git/info/exclude`.
