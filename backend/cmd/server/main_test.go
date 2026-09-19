package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestVersionFlagWritesToStdout(t *testing.T) {
	if os.Getenv("XIASS_TEST_VERSION_STDOUT") == "1" {
		flag.CommandLine = flag.NewFlagSet("xiass-api", flag.ContinueOnError)
		os.Args = []string{"xiass-api", "--version"}
		main()
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestVersionFlagWritesToStdout$") // #nosec G702 -- executes the current test binary with a fixed argument.
	cmd.Env = append(os.Environ(), "XIASS_TEST_VERSION_STDOUT=1")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("version subprocess failed: %v; stderr=%q", err, stderr.String())
	}

	want := fmt.Sprintf("XIASS API %s (commit: %s, built: %s)\n", Version, Commit, Date)
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}
