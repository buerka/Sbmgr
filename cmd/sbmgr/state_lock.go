package main

import (
	"errors"
	"fmt"
)

func (a *app) withStateLock(fn func() error) (err error) {
	if a.stateLockHeld {
		return fn()
	}
	lock, err := acquireStateFileLock(a.statePath + ".lock")
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
