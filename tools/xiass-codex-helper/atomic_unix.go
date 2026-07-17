//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

func replaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return nil
	}
	defer directory.Close()
	_ = directory.Sync()
	return nil
}
