package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// createStateDirectories creates the project's state directories and restricts
// access to their owner, including when the directories already exist.
func (project *Project) createStateDirectories() error {
	directories := []string{
		project.stateDir(),
		project.sandboxHomeDir(),
		project.sshDir(),
	}

	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		if err := os.Chmod(directory, 0700); err != nil {
			return err
		}
	}

	return nil
}

// acquireBuildLock waits for exclusive access to the project's build and SSH setup.
// Closing the returned lock releases it; cancellation interrupts the wait.
func (project *Project) acquireBuildLock(ctx context.Context) (*flock.Flock, error) {
	if err := project.createStateDirectories(); err != nil {
		return nil, err
	}

	buildLock := flock.New(filepath.Join(project.stateDir(), "build.lock"))
	if _, err := buildLock.TryLockContext(ctx, 100*time.Millisecond); err != nil {
		buildLock.Close()
		return nil, fmt.Errorf("lock development build: %w", err)
	}
	if err := ignoreStateInGit(project.RootDir); err != nil {
		buildLock.Close()
		return nil, err
	}

	return buildLock, nil
}
