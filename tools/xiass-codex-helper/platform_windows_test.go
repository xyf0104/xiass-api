//go:build windows

package main

import (
	"os"
	"strconv"
	"testing"
)

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

func TestWindowsPackagedExecutableDetection(t *testing.T) {
	packaged := `C:\Program Files\WindowsApps\OpenAI.Codex_26.707.12708.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe`
	if !isWindowsPackagedExecutable(packaged) {
		t.Fatalf("Windows Store Codex path was not detected: %s", packaged)
	}
	if isWindowsPackagedExecutable(`C:\Program Files\Codex\Codex.exe`) {
		t.Fatal("ordinary Codex installation was misclassified as a Store package")
	}
}

func TestWindowsNativeProcessLookupFindsCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processIDs, err := windowsProcessIDsWithError(executable)
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(os.Getpid())
	for _, processID := range processIDs {
		if processID == want {
			return
		}
	}
	t.Fatalf("current process %s was not found in %v", want, processIDs)
}

func TestHiddenWindowsCommandUsesNoWindowFlags(t *testing.T) {
	command := hiddenWindowsCommand("cmd.exe", "/c", "exit", "0")
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("hidden command is missing the Windows hide-window flag")
	}
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}
