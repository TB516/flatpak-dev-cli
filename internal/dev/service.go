package dev

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/gofrs/flock"
)

const idleTimeout = 30 * time.Second

// serviceUnitName identifies this checkout independently of its application ID or editor alias.
func (project *Project) serviceUnitName() string {
	projectHash := sha256.Sum256([]byte(project.RootDir))
	unitPrefix := defaultSSHHostName(project.RootDir)
	if len(unitPrefix) > 28 {
		unitPrefix = unitPrefix[:28]
	}
	return fmt.Sprintf("%s-%x.service", unitPrefix, projectHash[:6])
}

// isSandboxRunning reports whether systemd considers the project's service active.
func (project *Project) isSandboxRunning(ctx context.Context) bool {
	return exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", project.serviceUnitName()).Run() == nil
}

// useSandbox holds a shared lock for a caller's entire session and starts the sandbox if needed.
// The idle supervisor requires the exclusive lock before it can shut down.
func (project *Project) useSandbox(ctx context.Context) (string, *flock.Flock, error) {
	if err := project.createStateDirectories(); err != nil {
		return "", nil, err
	}
	usageLock := flock.New(filepath.Join(project.stateDir(), "use.lock"))
	if _, err := usageLock.TryRLockContext(ctx, 100*time.Millisecond); err != nil {
		usageLock.Close()
		return "", nil, err
	}
	// Short connections may begin and end between the supervisor's polling ticks.
	now := time.Now()
	if err := os.Chtimes(usageLock.Path(), now, now); err != nil {
		usageLock.Close()
		return "", nil, err
	}

	if err := project.startSandbox(ctx); err != nil {
		usageLock.Close()
		return "", nil, err
	}
	instance, err := os.ReadFile(filepath.Join(project.stateDir(), "instance"))
	if err != nil {
		usageLock.Close()
		return "", nil, err
	}
	return strings.TrimSpace(string(instance)), usageLock, nil
}

// releaseSandbox records the end of use before releasing the session's shared lock.
func releaseSandbox(usageLock *flock.Flock) {
	now := time.Now()
	_ = os.Chtimes(usageLock.Path(), now, now)
	_ = usageLock.Close()
}

// startSandbox reuses a ready sandbox or prepares and launches one under systemd.
// The caller holds a shared usage lock. Startup acquires the exclusive build lock.
func (project *Project) startSandbox(ctx context.Context) error {
	buildLock, err := project.acquireBuildLock(ctx)
	if err != nil {
		return err
	}
	defer buildLock.Close()

	instancePath := filepath.Join(project.stateDir(), "instance")
	if project.isSandboxRunning(ctx) {
		if instance, err := os.ReadFile(instancePath); err == nil && len(instance) > 0 {
			return nil
		}
		// Finish a previous supervisor's shutdown before reusing the unit name.
		if err := exec.CommandContext(ctx, "systemctl", "--user", "stop", project.serviceUnitName()).Run(); err != nil {
			return err
		}
	}
	if err := os.Remove(instancePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := project.loadManifest(ctx); err != nil {
		return err
	}
	if err := project.buildSDKImage(ctx); err != nil {
		return err
	}
	if err := project.buildOpenSSH(ctx); err != nil {
		return err
	}
	if err := project.writeSSHServerConfig(ctx); err != nil {
		return err
	}
	scriptPath := filepath.Join(project.stateDir(), "sandbox.sh")
	if err := writeTemplate(scriptPath, "sandbox.sh", nil); err != nil {
		return err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}
	builderPath, err := exec.LookPath("flatpak-builder")
	if err != nil {
		return err
	}
	serviceArgs := []string{
		"--user", "--unit=" + project.serviceUnitName(), "--collect", "--service-type=exec",
		"--working-directory=" + project.RootDir,
		"--description=flatpak-dev sandbox for " + project.RootDir,
		"--property=SyslogIdentifier=" + project.serviceUnitName(),
		// The supervisor stops Builder and lets its FUSE cleanup helper finish.
		"--property=KillMode=process",
	}
	for _, variableName := range []string{"SSH_AUTH_SOCK", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "TERM"} {
		if value := os.Getenv(variableName); value != "" {
			serviceArgs = append(serviceArgs, "--setenv="+variableName+"="+value)
		}
	}
	serviceArgs = append(serviceArgs, executablePath, "--project", project.RootDir, "_serve", "--", builderPath)
	serviceArgs = append(serviceArgs, project.sandboxCommandArgs([]string{"/bin/sh", scriptPath, instancePath})...)
	command := exec.CommandContext(ctx, "systemd-run", serviceArgs...)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("start sandbox service: %w", err)
	}

	startupDeadline := time.NewTimer(20 * time.Second)
	defer startupDeadline.Stop()
	pollTicker := time.NewTicker(100 * time.Millisecond)
	defer pollTicker.Stop()
	logCommand := shellescape.QuoteCommand([]string{"journalctl", "--user", "-t", project.serviceUnitName()})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-startupDeadline.C:
			return fmt.Errorf("sandbox did not become ready; run %s", logCommand)
		case <-pollTicker.C:
			if !project.isSandboxRunning(ctx) {
				return fmt.Errorf("sandbox exited during startup; run %s", logCommand)
			}
			if instance, err := os.ReadFile(instancePath); err == nil && len(instance) > 0 {
				return nil
			}
		}
	}
}

// ServeSandbox owns the background Builder process and stops it after all callers leave.
// File locks are released by the kernel if a caller exits unexpectedly.
func (project *Project) ServeSandbox(ctx context.Context, commandArgs []string) error {
	usageLock := flock.New(filepath.Join(project.stateDir(), "use.lock"))
	defer usageLock.Close()
	defer os.Remove(filepath.Join(project.stateDir(), "instance"))

	sandboxContext, stopSandbox := context.WithCancel(ctx)
	defer stopSandbox()
	command := exec.CommandContext(sandboxContext, commandArgs[0], commandArgs[1:]...)
	// Also tear down Builder if the supervisor exits without running its cleanup.
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	finished := make(chan struct{})
	var sandboxErr error
	go func() {
		sandboxErr = command.Wait()
		close(finished)
	}()
	defer func() {
		stopSandbox()
		<-finished
	}()

	lastUsed := time.Now()
	pollTicker := time.NewTicker(time.Second)
	defer pollTicker.Stop()
	for {
		select {
		case <-finished:
			if ctx.Err() != nil {
				return nil
			}
			return sandboxErr
		case <-ctx.Done():
			return nil
		case now := <-pollTicker.C:
			locked, err := usageLock.TryLock()
			if err != nil {
				return err
			}
			if !locked {
				lastUsed = now
				continue
			}
			if info, err := usageLock.Stat(); err == nil && info.ModTime().After(lastUsed) {
				lastUsed = info.ModTime()
			}
			if now.Sub(lastUsed) >= idleTimeout {
				// Keep the exclusive lock until the sandbox has exited and readiness is removed.
				return nil
			}
			if err := usageLock.Unlock(); err != nil {
				return err
			}
		}
	}
}
