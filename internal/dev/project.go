package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Project identifies one checkout and its normal Flatpak application manifest.
type Project struct {
	RootDir      string
	manifestPath string
	manifest     *applicationManifest
}

type buildOptions struct {
	Env                  map[string]string `json:"env"`
	AppendPath           string            `json:"append-path"`
	PrependPath          string            `json:"prepend-path"`
	AppendPkgConfigPath  string            `json:"append-pkg-config-path"`
	PrependPkgConfigPath string            `json:"prepend-pkg-config-path"`
	AppendLDLibraryPath  string            `json:"append-ld-library-path"`
	PrependLDLibraryPath string            `json:"prepend-ld-library-path"`
}

type manifestModule struct {
	Name         string       `json:"name"`
	BuildOptions buildOptions `json:"build-options"`
}

type applicationManifest struct {
	resolvedJSON   []byte
	ID             string           `json:"id"`
	Runtime        string           `json:"runtime"`
	RuntimeVersion string           `json:"runtime-version"`
	SDK            string           `json:"sdk"`
	Modules        []manifestModule `json:"modules"`
}

// loadManifest reads the application settings when a new sandbox needs preparing.
func (project *Project) loadManifest(ctx context.Context) error {
	var err error
	if project.manifestPath == "" {
		project.manifestPath, project.manifest, err = findApplicationManifest(ctx, project.RootDir)
	} else {
		project.manifest, err = readApplicationManifest(ctx, project.manifestPath)
	}
	return err
}

// findApplicationManifest looks in the checkout root and flatpak/ directory,
// excluding development manifests. Exactly one application manifest must match.
func findApplicationManifest(ctx context.Context, rootDir string) (string, *applicationManifest, error) {
	var manifestPaths []string
	var manifest *applicationManifest
	for _, searchDir := range []string{rootDir, filepath.Join(rootDir, "flatpak")} {
		entries, err := os.ReadDir(searchDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", nil, err
		}

		for _, entry := range entries {
			if entry.IsDir() || !hasManifestExtension(entry.Name()) || hasDevManifestSuffix(entry.Name()) {
				continue
			}

			manifestPath := filepath.Join(searchDir, entry.Name())
			candidate, err := readApplicationManifest(ctx, manifestPath)
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			if err == nil {
				manifestPaths = append(manifestPaths, manifestPath)
				manifest = candidate
			}
		}
	}

	switch len(manifestPaths) {
	case 0:
		return "", nil, fmt.Errorf("no Flatpak application manifest found in %s or its flatpak/ directory; pass --manifest", rootDir)
	case 1:
		return manifestPaths[0], manifest, nil
	default:
		return "", nil, fmt.Errorf("multiple Flatpak manifests found: %s; pass --manifest", strings.Join(manifestPaths, ", "))
	}
}

// hasManifestExtension reports whether a filename has a JSON or YAML extension.
func hasManifestExtension(fileName string) bool {
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".yml", ".yaml", ".json":
		return true
	default:
		return false
	}
}

// hasDevManifestSuffix recognizes .dev and .devel before the file extension.
func hasDevManifestSuffix(fileName string) bool {
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	return strings.HasSuffix(stem, ".dev") || strings.HasSuffix(stem, ".devel")
}

// readApplicationManifest asks Flatpak Builder to resolve manifest includes,
// then reads the application, SDK, and module settings from its JSON output.
func readApplicationManifest(ctx context.Context, manifestPath string) (*applicationManifest, error) {
	// flatpak-builder writes a .flatpak-builder directory in its working
	// directory even for --show-manifest. Inspect in temporary space so read-only
	// commands do not create project state.
	inspectionDir, err := os.MkdirTemp("", "flatpak-dev-manifest-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(inspectionDir)

	command := exec.CommandContext(ctx, "flatpak-builder", "--show-manifest", manifestPath)
	command.Dir = inspectionDir
	manifestJSON, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("read Flatpak manifest %s: %s", manifestPath, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("read Flatpak manifest %s: %w", manifestPath, err)
	}

	var manifest applicationManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("parse Flatpak manifest %s: %w", manifestPath, err)
	}
	if manifest.ID == "" || manifest.Runtime == "" || manifest.RuntimeVersion == "" || manifest.SDK == "" {
		return nil, fmt.Errorf("%s is missing id, runtime, runtime-version, or sdk", manifestPath)
	}

	manifest.resolvedJSON = manifestJSON
	return &manifest, nil
}

// stateDir returns the directory for this checkout's generated development files.
func (project *Project) stateDir() string { return filepath.Join(project.RootDir, ".flatpak-dev") }

// buildDir returns the Flatpak build directory used to launch the SDK sandbox.
func (project *Project) buildDir() string { return filepath.Join(project.stateDir(), "build") }

// sandboxHomeDir returns the sandbox home that persists across builds and sessions.
func (project *Project) sandboxHomeDir() string { return filepath.Join(project.stateDir(), "home") }

// sshDir returns the directory containing generated OpenSSH configuration and keys.
func (project *Project) sshDir() string { return filepath.Join(project.stateDir(), "ssh") }

// toolsDir returns the separate Flatpak build containing OpenSSH.
func (project *Project) toolsDir() string { return filepath.Join(project.stateDir(), "tools") }

// profilePath returns the generated shell setup shared by commands and SSH sessions.
func (project *Project) profilePath() string {
	return filepath.Join(project.stateDir(), "session.profile")
}

// OpenProject resolves paths without reading a manifest or creating project state.
// Joining a running sandbox does not require its manifest to remain valid.
func OpenProject(projectDir, manifestPath string) (*Project, error) {
	if projectDir == "" {
		projectDir = "."
	}

	rootDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	rootDir, err = filepath.EvalSymlinks(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}

	directoryInfo, err := os.Stat(rootDir)
	if err != nil {
		return nil, err
	}
	if !directoryInfo.IsDir() {
		return nil, fmt.Errorf("project path %q is not a directory", rootDir)
	}

	if manifestPath != "" && !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(rootDir, manifestPath)
	}
	return &Project{RootDir: rootDir, manifestPath: manifestPath}, nil
}
