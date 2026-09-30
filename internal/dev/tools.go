package dev

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/creachadair/atomicfile"
)

//go:embed openssh.json
var opensshModule json.RawMessage

// buildOpenSSH builds the tool's own recipe against the application's SDK.
// Builder caches the compilation; the application manifest never includes this module.
func (project *Project) buildOpenSSH(ctx context.Context) error {
	manifest := struct {
		ID             string            `json:"id"`
		Runtime        string            `json:"runtime"`
		RuntimeVersion string            `json:"runtime-version"`
		SDK            string            `json:"sdk"`
		Modules        []json.RawMessage `json:"modules"`
	}{
		ID:             "dev.flatpak.Tools",
		Runtime:        project.manifest.Runtime,
		RuntimeVersion: project.manifest.RuntimeVersion,
		SDK:            project.manifest.SDK,
		Modules:        []json.RawMessage{opensshModule},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(project.stateDir(), "tools.json")
	if err := atomicfile.WriteData(manifestPath, manifestJSON, 0600); err != nil {
		return err
	}

	command := exec.CommandContext(ctx, "flatpak-builder",
		"--user", "--force-clean", "--build-only", "--rebuild-on-sdk-change",
		"--state-dir="+filepath.Join(project.stateDir(), "tools-cache"), project.toolsDir(), manifestPath)
	command.Dir = project.stateDir()
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("build OpenSSH tools: %w", err)
	}
	return nil
}
