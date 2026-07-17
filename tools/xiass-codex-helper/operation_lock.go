package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func acquireLifecycleLock(codexHome string) (func(), error) {
	path := filepath.Join(codexHome, "xiass-helper", "lifecycle.lock")
	return acquireProcessLock(path, "another XIASS Codex Helper is already changing this Codex installation")
}

func acquireProcessLock(path, busyMessage string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFileNonBlocking(lock); err != nil {
		_ = lock.Close()
		return nil, errors.New(busyMessage)
	}
	if err := lock.Truncate(0); err != nil {
		_ = unlockFile(lock)
		_ = lock.Close()
		return nil, err
	}
	if _, err := lock.Seek(0, 0); err != nil {
		_ = unlockFile(lock)
		_ = lock.Close()
		return nil, err
	}
	if _, err := lock.WriteString(fmt.Sprintf("%d\n", os.Getpid())); err != nil {
		_ = unlockFile(lock)
		_ = lock.Close()
		return nil, err
	}
	if err := lock.Sync(); err != nil {
		_ = unlockFile(lock)
		_ = lock.Close()
		return nil, err
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = unlockFile(lock)
		_ = lock.Close()
	}, nil
}
