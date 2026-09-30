package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/TB516/flatpak-dev-cli/internal/dev"
	"github.com/urfave/cli/v3"
)

// main runs the CLI and reports setup or connection errors.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if err := newRootCommand().Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "flatpak-dev:", err)
		os.Exit(1)
	}
}

// newRootCommand defines the public commands and the two internal process entrypoints.
func newRootCommand() *cli.Command {
	var projectDir, manifestPath, hostName string
	var project *dev.Project
	executableArgPosition := 1

	return &cli.Command{
		Name:           "flatpak-dev",
		Usage:          "Run commands and SSH editors in one shared Flatpak SDK sandbox",
		Description:    "The sandbox starts automatically and stops 30 seconds after its last command or connection ends.",
		ErrWriter:      io.Discard,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Usage: "Project directory", Destination: &projectDir},
			&cli.StringFlag{Name: "manifest", Usage: "Flatpak application manifest", Destination: &manifestPath},
		},
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			var err error
			project, err = dev.OpenProject(projectDir, manifestPath)
			if err != nil {
				return nil, err
			}
			return ctx, nil
		},
		Commands: []*cli.Command{
			{
				Name:         "run",
				Usage:        "Run a command in the shared sandbox",
				ArgsUsage:    "-- COMMAND [ARGS...]",
				StopOnNthArg: &executableArgPosition,
				Action: func(ctx context.Context, command *cli.Command) error {
					if command.NArg() == 0 {
						return fmt.Errorf("run needs a command after --")
					}
					return project.RunInSandbox(ctx, command.Args().Slice())
				},
			},
			{
				Name:  "ssh",
				Usage: "Connect a terminal or configure an SSH editor",
				Commands: []*cli.Command{
					{
						Name:         "connect",
						Usage:        "Start or join the sandbox with an SSH shell",
						ArgValidator: rejectPositionalArguments,
						Action: func(ctx context.Context, _ *cli.Command) error {
							return project.ConnectSSH(ctx)
						},
					},
					{
						Name:         "config",
						Usage:        "Write an editor host entry and print its SSH Include instruction",
						ArgValidator: rejectPositionalArguments,
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "host", Usage: "SSH host name", Destination: &hostName},
						},
						Action: func(ctx context.Context, command *cli.Command) error {
							config, err := project.ConfigureSSH(ctx, hostName)
							if err != nil {
								return err
							}
							fmt.Fprintf(command.Writer, "SSH host: %s\nOpen project: %s\n", config.HostName, project.RootDir)
							fmt.Fprintf(command.Writer, "Add this line near the top of ~/.ssh/config once:\n  %s\n", config.IncludeLine)
							return nil
						},
					},
				},
			},
			{
				Name:         "_connect",
				Hidden:       true,
				ArgValidator: rejectPositionalArguments,
				Action: func(ctx context.Context, _ *cli.Command) error {
					return project.ProxySSHConnection(ctx)
				},
			},
			{
				Name:         "_serve",
				Hidden:       true,
				StopOnNthArg: &executableArgPosition,
				Action: func(ctx context.Context, command *cli.Command) error {
					if command.NArg() == 0 {
						return fmt.Errorf("_serve needs a sandbox command")
					}
					return project.ServeSandbox(ctx, command.Args().Slice())
				},
			},
		},
	}
}

// rejectPositionalArguments rejects extra arguments for commands that accept only flags.
func rejectPositionalArguments(_ context.Context, command *cli.Command) error {
	if command.NArg() != 0 {
		return fmt.Errorf("%s does not take arguments", command.FullName())
	}
	return nil
}
