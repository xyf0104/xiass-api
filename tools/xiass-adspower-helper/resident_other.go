//go:build !darwin

package main

import "errors"

func installResidentHelper(*config) error {
	return errors.New("resident installation is currently available on macOS")
}
