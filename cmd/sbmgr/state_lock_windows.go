//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type stateFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireStateFileLock(path string) (*stateFileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	lock := &stateFileLock{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &lock.overlapped); err != nil {
		file.Close()
		return nil, err
	}
	return lock, nil
}

func (l *stateFileLock) release() error {
	unlockErr := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
