# flatpak-dev

`flatpak-dev` is a planned CLI for working on a Flatpak project inside its own SDK environment. The goal is to let SSH-capable editors and terminals use that environment without adding connection scripts or an SSH server module to each application repository.

The project is currently a Go CLI scaffold. It can display help, but it cannot prepare a Flatpak environment or accept SSH connections yet.

For now, run `flatpak-dev --help` after building it. Development setup and checks are in [the development guide](docs/development.md).
