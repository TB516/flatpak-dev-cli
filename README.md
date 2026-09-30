# flatpak-dev

`flatpak-dev` runs commands and SSH editors inside a shared Flatpak SDK sandbox for each project. It uses your application's manifest and an optional `flatpak/.profile`, and prepares SSH separately from the application build.

This Linux prototype needs Flatpak, Flatpak Builder, a systemd user session, and the OpenSSH client tools on the host. Install the project's SDK and extensions first.

## Install

From the project where you want to use it, install with mise:

```sh
mise use github:TB516/flatpak-dev-cli@0.1.0
mise exec -- flatpak-dev --version
```

This pins the tool in the project's `mise.toml`. With mise shell activation, `flatpak-dev` is available when you enter that project. Otherwise, prefix commands with `mise exec --`.

[GitHub Releases](https://github.com/TB516/flatpak-dev-cli/releases) provide Linux x64 and ARM64 binaries. See [development setup](docs/development.md) to build from source.

## Run a command

From the project directory:

```sh
flatpak-dev run -- pnpm test
```

The first command builds the application's dependencies and OpenSSH with the project's SDK. Flatpak Builder caches both builds. It skips the final application module so you can build and run your live checkout with your usual commands.

## Open a terminal

```sh
flatpak-dev ssh connect
```

This starts the sandbox if needed and opens a shell inside it. Commands, terminals, and editors all join the same running sandbox, with the same dependencies, project files, and persistent home.

## Connect an editor

Generate an SSH host entry:

```sh
flatpak-dev ssh config
```

Add the printed `Include` line near the top of `~/.ssh/config` once. Then connect to the printed host with Zed, VS Code, or another SSH editor and open the printed project path. Connecting starts the sandbox automatically.

After upgrading the CLI, run `ssh config` again so the editor entry points to the newly installed binary.

Generated host entries live under `$XDG_DATA_HOME/flatpak-dev/ssh`, defaulting to `~/.local/share/flatpak-dev/ssh`.

OpenSSH provides terminals, file transfers used by editors, and TCP and Unix socket forwarding. The connection uses a local process, so there is no SSH listening port to manage.

## Sandbox lifetime

The sandbox runs in the background through your systemd user session. It stays alive while any command or SSH connection is open, even when a command produces no output. It shuts down 30 seconds after the last command or connection ends. Reconnecting during that grace period reuses it.

Keep a connection open while you need background jobs. Detached jobs end when the sandbox shuts down. An editor that retains an SSH connection also keeps the sandbox running.

Dependencies refresh from the manifest when a new sandbox starts. If you change the manifest while it is running, close its connections and let it stop before reconnecting. The persistent home and build caches survive shutdown.

Startup errors include a command to read the service logs. For other troubleshooting, see [service inspection](docs/development.md#service-inspection).

## Choose a project

The CLI looks for the application manifest in the checkout root or `flatpak/`. If there is more than one, pass `--manifest path/to/app.yml`. Use that flag with `ssh config` too so editor connections retain the choice.

Use `--project path/to/project` to work from elsewhere. Each checkout has its own sandbox, so several projects can run at once. If two checkouts have the same directory name, use `ssh config --host my-project` to choose a distinct editor host.

Append `--help` to a command to see its options.

## Project profile

The CLI loads `flatpak/.profile` for commands and SSH shell sessions. Use it to configure development tools. For example:

```sh
export PATH="/usr/lib/sdk/node24/bin:$PATH";
export PNPM_CONFIG_STORE_DIR="$HOME/.local/share/pnpm/store";
```

Profile changes apply to the next command or shell session. Keep SDK extensions and application build dependencies in the normal Flatpak manifest. Avoid printing output from the profile, since editors and file transfers use noninteractive sessions too.

Generated files, build caches, and the sandbox home live under `.flatpak-dev/`. The CLI excludes that directory through the checkout's local Git exclude file.
