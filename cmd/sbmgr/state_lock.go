package main

import (
	"errors"
	"fmt"
	"path/filepath"
)

func stateLockPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "state.lock")
}

func (a *app) withStateLock(fn func() error) (err error) {
	if a.stateLockHeld {
		return fn()
	}
	// The lock is tied to the application directory, not the storage filename.
	// That makes legacy state.json and state.db processes serialize against the
	// same read-modify-write boundary during migration.
	lock, err := acquireStateFileLock(stateLockPath(a.statePath))
	if err != nil {
		return fmt.Errorf("锁定状态文件: %w", err)
	}
	a.stateLockHeld = true
	defer func() {
		a.stateLockHeld = false
		if releaseErr := lock.release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("释放状态文件锁: %w", releaseErr))
		}
	}()
	return fn()
}
