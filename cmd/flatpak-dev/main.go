package main

import (
	"fmt"
	"os"
)

const usage = `flatpak-dev prepares Flatpak projects for SSH-based development.

Usage:
  flatpak-dev help
  flatpak-dev --help

Environment commands are not available yet.
`

func main() {
	if len(os.Args) == 1 {
		fmt.Fprint(os.Stdout, usage)
		return
	}

	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "help", "-h", "--help":
			fmt.Fprint(os.Stdout, usage)
			return
		}
	}

	fmt.Fprintf(os.Stderr, "flatpak-dev: unknown arguments: %q\n\n%s", os.Args[1:], usage)
	os.Exit(2)
}
