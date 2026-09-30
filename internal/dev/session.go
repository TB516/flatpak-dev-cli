package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"al.essio.dev/pkg/shellescape"
)

// RunInSandbox passes literal command arguments through SSH and preserves its exit status.
// flatpak enter itself does not propagate the entered command's exit code.
func (project *Project) RunInSandbox(ctx context.Context, commandArgs []string) error {
	return project.runSSH(ctx, []string{"-T"}, []string{shellescape.QuoteCommand(commandArgs)})
}

// ConnectSSH opens an interactive shell in the same sandbox used by commands and editors.
func (project *Project) ConnectSSH(ctx context.Context) error {
	return project.runSSH(ctx, nil, nil)
}

// runSSH prepares the client; its ProxyCommand handles sandbox startup for every connection.
func (project *Project) runSSH(ctx context.Context, options, commandArgs []string) error {
	configPath := filepath.Join(project.sshDir(), "client.conf")
	if err := project.writeSSHClientConfig(ctx, configPath, "sandbox"); err != nil {
		return err
	}
	args := append([]string{"-F", configPath}, options...)
	args = append(args, "sandbox")
	args = append(args, commandArgs...)
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return err
	}
	// Replace the CLI so SSH owns the terminal, signals, and command exit status.
	return syscall.Exec(sshPath, append([]string{"ssh"}, args...), os.Environ())
}

// ProxySSHConnection runs one OpenSSH handler over stdin/stdout inside the shared sandbox.
func (project *Project) ProxySSHConnection(ctx context.Context) error {
	instance, usageLock, err := project.useSandbox(ctx)
	if err != nil {
		return err
	}
	defer releaseSandbox(usageLock)
	// Keep a lock inside the sandbox too, so losing the host wrapper cannot stop an active handler.
	args := []string{"enter", instance, "flock", "--shared", usageLock.Path(),
		filepath.Join(project.toolsDir(), "files", "sbin", "sshd"),
		"-i", "-e", "-f", filepath.Join(project.sshDir(), "sshd_config")}
	command := exec.CommandContext(ctx, "flatpak", args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	err = command.Run()
	// SSH terminates its proxy when the connection closes, including after success.
	if ctx.Err() != nil {
		return nil
	}
	return err
}
