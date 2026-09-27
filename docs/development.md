# Development

Install the project toolchain with `mise install`. The Go version is pinned in `mise.toml`; no global Go installation is needed.

Run these commands from the repository root:

```sh
mise run fmt
mise run check
mise run build
./bin/flatpak-dev --help
```

`fmt` uses Go's `gofmt`. `check` verifies formatting, then runs `go vet` and `go test`. `build` writes the CLI to the ignored `bin/` directory.
