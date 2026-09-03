//go:build !windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type stateFileLock struct {
	file *os.File
}

func acquireStateFileLock(path string) (*stateFileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return &stateFileLock{file: file}, nil
}

func (l *stateFileLock) release() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
