package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const stateLockWaitLimit = 30 * time.Second

func stateLockPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "state.lock")
}

func (a *app) withStateLock(fn func() error) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), stateLockWaitLimit)
	defer cancel()
	return a.withStateLockContext(ctx, fn)
}

// app is a single operation/worker context. A nested call on the same context
// may reuse its lock, but concurrent workers must use distinct app instances.
// The deadline bounds acquisition only: once a transaction starts, its actual
// commit/rollback result must be reported, never a speculative timeout.
func (a *app) withStateLockContext(ctx context.Context, fn func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.stateLockHeld {
		return fn()
	}
	// The lock is tied to the application directory, not the storage filename.
	// That makes legacy state.json and state.db processes serialize against the
	// same read-modify-write boundary during migration.
	finishWait := startPerformancePhase("lock.wait")
	lock, err := acquireStateFileLockContext(ctx, stateLockPath(a.statePath))
	finishWait(err)
	if err != nil {
		return fmt.Errorf("等待状态锁失败，请稍后重试: %w", err)
	}
	finishHold := startPerformancePhase("lock.hold")
	a.stateLockHeld = true
	defer func() {
		a.stateLockHeld = false
		if releaseErr := lock.release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("释放状态文件锁: %w", releaseErr))
		}
		finishHold(err)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
