//go:build windows

package main

import "testing"

func TestNormalizeWindowsExecutable(t *testing.T) {
	got := normalizeWindowsExecutable(`"C:\Users\Test User\AppData\Local\Programs\Codex\Codex.exe",0`)
	want := `C:\Users\Test User\AppData\Local\Programs\Codex\Codex.exe`
	if got != want {
		t.Fatalf("normalizeWindowsExecutable() = %q, want %q", got, want)
	}
}

func TestWindowsCodexExecutableRejectsCommonCLIPaths(t *testing.T) {
	for _, candidate := range []string{
		`C:\Users\Test\AppData\Roaming\npm\codex.exe`,
		`C:\Users\Test\.cargo\bin\codex.exe`,
		`C:\Users\Test\scoop\apps\codex\current\codex.exe`,
	} {
		if isWindowsCodexExecutable(candidate) {
			t.Fatalf("CLI path was detected as Codex App: %s", candidate)
		}
	}
}
