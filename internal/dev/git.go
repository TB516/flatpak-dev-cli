package dev

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ignoreStateInGit excludes generated project state using Git's local exclude file.
// It leaves tracked ignore rules alone and does nothing outside a Git checkout.
func ignoreStateInGit(rootDir string) error {
	excludePathOutput, err := exec.Command("git", "-C", rootDir, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return nil // The project need not be a Git checkout.
	}

	checkIgnoreCommand := exec.Command("git", "-C", rootDir, "check-ignore", "--quiet", "--", ".flatpak-dev/build.lock")
	if err := checkIgnoreCommand.Run(); err == nil {
		return nil
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		return fmt.Errorf("check whether Git ignores .flatpak-dev: %w", err)
	}

	excludePath := strings.TrimSpace(string(excludePathOutput))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(rootDir, excludePath)
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0700); err != nil {
		return err
	}

	excludeFile, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := excludeFile.WriteString("\n# flatpak-dev local state\n.flatpak-dev/\n"); err != nil {
		excludeFile.Close()
		return err
	}
	return excludeFile.Close()
}
