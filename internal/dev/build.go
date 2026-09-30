package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/creachadair/atomicfile"
)

// buildSDKImage prepares application dependencies and shell settings using Builder's cache.
// The caller must hold the build lock and check that the sandbox is stopped.
func (project *Project) buildSDKImage(ctx context.Context) error {
	builderArgs := []string{
		"--user", "--force-clean", "--build-only", "--rebuild-on-sdk-change",
		"--state-dir=" + filepath.Join(project.stateDir(), "builder-state"),
	}
	if len(project.manifest.Modules) > 0 {
		appModule := project.manifest.Modules[len(project.manifest.Modules)-1]
		if appModule.Name == "" {
			return fmt.Errorf("last module in %s has no name", project.manifestPath)
		}
		builderArgs = append(builderArgs, "--stop-at="+appModule.Name)
	}
	builderArgs = append(builderArgs, project.buildDir(), project.manifestPath)

	command := exec.CommandContext(ctx, "flatpak-builder", builderArgs...)
	command.Dir = project.stateDir()
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("build SDK environment: %w", err)
	}
	if err := project.writeRunManifest(); err != nil {
		return err
	}
	return project.writeSessionProfile(ctx)
}

// sandboxCommandArgs lets Builder supply the manifest environment and permissions,
// adding the live checkout, persistent home, and host integration used for development.
func (project *Project) sandboxCommandArgs(commandArgs []string) []string {
	builderArgs := []string{
		"--run", "--state-dir=" + filepath.Join(project.stateDir(), "builder-state"),
		"--share=network", "--share=ipc", "--allow=devel",
		"--socket=wayland", "--socket=fallback-x11", "--device=dri",
		"--filesystem=" + project.RootDir,
		"--env=HOME=" + project.sandboxHomeDir(),
		"--env=SHELL=/bin/bash",
		"--env=XDG_CONFIG_HOME=" + filepath.Join(project.sandboxHomeDir(), ".config"),
		"--env=XDG_CACHE_HOME=" + filepath.Join(project.sandboxHomeDir(), ".cache"),
		"--env=XDG_DATA_HOME=" + filepath.Join(project.sandboxHomeDir(), ".local", "share"),
		"--env=XDG_STATE_HOME=" + filepath.Join(project.sandboxHomeDir(), ".local", "state"),
		"--env=FLATPAK_DEV_PROJECT_ROOT=" + project.RootDir,
		"--env=FLATPAK_ID=" + project.manifest.ID,
	}
	if agentSocket := os.Getenv("SSH_AUTH_SOCK"); agentSocket != "" {
		builderArgs = append(builderArgs, "--filesystem="+filepath.Dir(agentSocket), "--env=SSH_AUTH_SOCK="+agentSocket)
	}
	builderArgs = append(builderArgs, project.buildDir(), filepath.Join(project.stateDir(), "run.json"))
	return append(builderArgs, commandArgs...)
}

// writeSessionProfile captures Builder's environment for OpenSSH, which otherwise
// resets it. Final-module overrides and the live project profile apply to every session.
func (project *Project) writeSessionProfile(ctx context.Context) error {
	command := exec.CommandContext(ctx, "flatpak-builder", project.sandboxCommandArgs([]string{"/usr/bin/env", "-0"})...)
	command.Dir = project.RootDir
	command.Stderr = os.Stderr
	environmentOutput, err := command.Output()
	if err != nil {
		return err
	}

	sessionEnvironment := make(map[string]string)
	for entry := range strings.SplitSeq(string(environmentOutput), "\x00") {
		variableName, value, found := strings.Cut(entry, "=")
		if found {
			sessionEnvironment[variableName] = value
		}
	}
	if len(project.manifest.Modules) > 0 {
		moduleOptions := project.manifest.Modules[len(project.manifest.Modules)-1].BuildOptions
		maps.Copy(sessionEnvironment, moduleOptions.Env)
		applyPathOption(sessionEnvironment, "PATH", moduleOptions.PrependPath, moduleOptions.AppendPath)
		applyPathOption(sessionEnvironment, "PKG_CONFIG_PATH", moduleOptions.PrependPkgConfigPath, moduleOptions.AppendPkgConfigPath)
		applyPathOption(sessionEnvironment, "LD_LIBRARY_PATH", moduleOptions.PrependLDLibraryPath, moduleOptions.AppendLDLibraryPath)
	}

	// Preserve each shell's working directory, depth, and requested terminal type.
	for _, name := range []string{"PWD", "OLDPWD", "SHLVL", "_", "TERM", "BASH_ENV", "ENV"} {
		delete(sessionEnvironment, name)
	}
	settings := struct {
		Environment map[string]string
		ProfilePath string
	}{Environment: sessionEnvironment, ProfilePath: project.profilePath()}
	return writeTemplate(project.profilePath(), "session.profile", settings)
}

// applyPathOption adds a module's path entries to Builder's existing environment.
func applyPathOption(environment map[string]string, variableName, prependPath, appendPath string) {
	pathValue := environment[variableName]
	if prependPath != "" {
		pathValue = prependPath + ":" + pathValue
	}
	if appendPath != "" {
		pathValue += ":" + appendPath
	}

	if prependPath != "" || appendPath != "" {
		environment[variableName] = pathValue
	}
}

// writeRunManifest adds process-lifetime and read-only flags to Builder's resolved
// manifest. --run accepts these only through build-options, not command-line flags.
func (project *Project) writeRunManifest() error {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(project.manifest.resolvedJSON, &manifest); err != nil {
		return err
	}
	options := make(map[string]json.RawMessage)
	if encodedOptions := manifest["build-options"]; len(encodedOptions) > 0 {
		if err := json.Unmarshal(encodedOptions, &options); err != nil {
			return err
		}
	}
	if options == nil {
		options = make(map[string]json.RawMessage)
	}
	var buildArgs []string
	if encodedArgs := options["build-args"]; len(encodedArgs) > 0 {
		if err := json.Unmarshal(encodedArgs, &buildArgs); err != nil {
			return err
		}
	}
	buildArgs = append(buildArgs, "--die-with-parent", "--readonly")
	options["build-args"], _ = json.Marshal(buildArgs)
	manifest["build-options"], _ = json.Marshal(options)
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteData(filepath.Join(project.stateDir(), "run.json"), manifestJSON, 0600)
}
