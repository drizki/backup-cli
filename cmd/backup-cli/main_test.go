package main

import (
	"os"
	"testing"
)

func TestRunVersionAndUsage(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()

	os.Args = []string{"backup-cli", "version"}
	if code := run(); code != 0 {
		t.Fatalf("version exit %d", code)
	}
	os.Args = []string{"backup-cli", "no-such-command"}
	if code := run(); code != 2 {
		t.Fatalf("unknown command exit %d, want 2", code)
	}
}
