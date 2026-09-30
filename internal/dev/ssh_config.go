package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/creachadair/atomicfile"
	"github.com/gofrs/flock"
)

var (
	validSSHHostName          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
	invalidHostNameCharacters = regexp.MustCompile(`[^A-Za-z0-9_-]`)
)

// SSHHostConfig reports the editor host and the SSH Include needed to use it.
type SSHHostConfig struct {
	HostName    string
	IncludeLine string
}

// ConfigureSSH writes an editor host entry without starting the sandbox.
func (project *Project) ConfigureSSH(ctx context.Context, hostName string) (*SSHHostConfig, error) {
	if hostName == "" {
		hostName = defaultSSHHostName(project.RootDir)
	}
	if !validSSHHostName.MatchString(hostName) {
		return nil, fmt.Errorf("invalid SSH host name %q", hostName)
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(dataDir) {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(homeDir, ".local", "share")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtimeDir) {
		return nil, fmt.Errorf("XDG_RUNTIME_DIR must be an absolute path; run ssh config from a user session")
	}
	sshConfigDir := filepath.Join(dataDir, "flatpak-dev", "ssh")
	lockDir := filepath.Join(runtimeDir, "flatpak-dev")
	for _, directory := range []string{sshConfigDir, lockDir} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, err
		}
	}
	// Serialize alias ownership checks across projects that share this config directory.
	configLock := flock.New(filepath.Join(lockDir, "ssh-config.lock"))
	defer configLock.Close()
	if _, err := configLock.TryLockContext(ctx, 100*time.Millisecond); err != nil {
		return nil, fmt.Errorf("lock editor SSH config: %w", err)
	}

	hostConfigPath := filepath.Join(sshConfigDir, hostName+".conf")
	if err := project.writeSSHClientConfig(ctx, hostConfigPath, hostName); err != nil {
		return nil, err
	}
	return &SSHHostConfig{
		HostName:    hostName,
		IncludeLine: "Include " + sshConfigQuote(filepath.Join(sshConfigDir, "*")),
	}, nil
}

// writeSSHClientConfig prepares the key and either a private CLI config or an editor entry.
// SSH loads the identity before starting its proxy, so the key must already exist.
func (project *Project) writeSSHClientConfig(ctx context.Context, hostConfigPath, hostName string) error {
	buildLock, err := project.acquireBuildLock(ctx)
	if err != nil {
		return err
	}
	defer buildLock.Close()

	if _, err := ensureSSHKey(ctx, filepath.Join(project.sshDir(), "id_ed25519")); err != nil {
		return err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}
	userInfo, err := user.Current()
	if err != nil {
		return err
	}
	for _, path := range []string{project.RootDir, project.manifestPath, executablePath, hostConfigPath} {
		if strings.ContainsAny(path, "\r\n") {
			return fmt.Errorf("SSH configuration does not support paths containing line breaks")
		}
	}

	projectMarker := "# flatpak-dev project: " + project.RootDir + "\n"
	if existingConfig, err := os.ReadFile(hostConfigPath); err == nil {
		if !strings.HasPrefix(string(existingConfig), projectMarker) {
			return fmt.Errorf("SSH host %q already belongs to another project; choose --host", hostName)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	settings := struct {
		RootDir, HostName, Username                string
		ProxyArgs                                  []string
		IdentityFile, KnownHostsFile, HostKeyAlias string
	}{
		RootDir:        project.RootDir,
		HostName:       hostName,
		Username:       userInfo.Username,
		ProxyArgs:      []string{executablePath, "_connect", "--project", project.RootDir, "--manifest", project.manifestPath},
		IdentityFile:   filepath.Join(project.sshDir(), "id_ed25519"),
		KnownHostsFile: filepath.Join(project.sshDir(), "known_hosts"),
		HostKeyAlias:   project.serviceUnitName(),
	}
	return writeTemplate(hostConfigPath, "ssh_config", settings)
}

// writeSSHServerConfig prepares keys and configuration for per-connection OpenSSH handlers.
func (project *Project) writeSSHServerConfig(ctx context.Context) error {
	userInfo, err := user.Current()
	if err != nil {
		return err
	}
	clientKey, err := ensureSSHKey(ctx, filepath.Join(project.sshDir(), "id_ed25519"))
	if err != nil {
		return err
	}
	hostKeyPath := filepath.Join(project.sshDir(), "host_ed25519")
	hostKey, err := ensureSSHKey(ctx, hostKeyPath)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteData(filepath.Join(project.sshDir(), "authorized_keys"), clientKey, 0600); err != nil {
		return err
	}
	knownHost := append([]byte(project.serviceUnitName()+" "), hostKey...)
	if err := atomicfile.WriteData(filepath.Join(project.sshDir(), "known_hosts"), knownHost, 0600); err != nil {
		return err
	}

	toolsFiles := filepath.Join(project.toolsDir(), "files")
	sessionPath := filepath.Join(project.sshDir(), "session.sh")
	if err := writeTemplate(sessionPath, "session.sh", nil); err != nil {
		return err
	}
	settings := struct {
		HostKey, AuthorizedKeys, Username string
		SessionHelper, AuthHelper         string
		SessionArgs                       []string
		Home, Profile                     string
	}{
		HostKey:        hostKeyPath,
		AuthorizedKeys: filepath.Join(project.sshDir(), "authorized_keys"),
		Username:       userInfo.Username,
		SessionHelper:  filepath.Join(toolsFiles, "libexec", "sshd-session"),
		AuthHelper:     filepath.Join(toolsFiles, "libexec", "sshd-auth"),
		SessionArgs:    []string{"/bin/sh", sessionPath, filepath.Join(toolsFiles, "libexec", "sftp-server")},
		Home:           "HOME=" + project.sandboxHomeDir(),
		Profile:        "BASH_ENV=" + project.profilePath(),
	}
	return writeTemplate(filepath.Join(project.sshDir(), "sshd_config"), "sshd_config", settings)
}

// ensureSSHKey uses the host's ssh-keygen to create a key if missing and returns its public key.
func ensureSSHKey(ctx context.Context, keyPath string) ([]byte, error) {
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		command := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath)
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "ssh-keygen", "-y", "-f", keyPath)
	command.Stderr = os.Stderr
	return command.Output()
}

// defaultSSHHostName derives an SSH alias from the checkout's directory name,
// replacing unsupported characters with hyphens.
func defaultSSHHostName(rootDir string) string {
	return "flatpak-" + invalidHostNameCharacters.ReplaceAllString(filepath.Base(rootDir), "-")
}

// escapeSSHTokens keeps literal percent signs from becoming OpenSSH expansion tokens.
func escapeSSHTokens(value string) string {
	return strings.ReplaceAll(value, "%", "%%")
}

// sshConfigQuote escapes backslashes and quotes, then wraps an SSH config value
// in double quotes so spaces remain part of the value.
func sshConfigQuote(value string) string {
	escapedValue := strings.ReplaceAll(value, `\`, `\\`)
	escapedValue = strings.ReplaceAll(escapedValue, `"`, `\"`)
	return `"` + escapedValue + `"`
}
