package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOSProcessLockIgnoresStaleContentsAndSerializesOwners(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "xiass-helper", "lifecycle.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := acquireLifecycleLock(home)
	if err != nil {
		t.Fatalf("stale lock-file contents blocked the OS lock: %v", err)
	}
	if _, err := acquireLifecycleLock(home); err == nil {
		t.Fatal("a second owner acquired the same OS lock concurrently")
	}
	release()
	secondRelease, err := acquireLifecycleLock(home)
	if err != nil {
		t.Fatalf("OS lock was not released when the first owner closed: %v", err)
	}
	secondRelease()
}
