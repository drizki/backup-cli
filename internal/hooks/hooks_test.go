package hooks

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunPassesEnvironment(t *testing.T) {
	out, err := Run(context.Background(), `echo "$BACKUP_CLI_JOB:$BACKUP_CLI_EXIT_CODE"`, []string{"BACKUP_CLI_JOB=photos", "BACKUP_CLI_EXIT_CODE=3"}, time.Minute)
	if err != nil || strings.TrimSpace(out) != "photos:3" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestRunFailureReportsOutput(t *testing.T) {
	out, err := Run(context.Background(), "echo oops >&2; exit 7", nil, time.Minute)
	if err == nil || !strings.Contains(out, "oops") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	_, err := Run(context.Background(), "sleep 30 & sleep 30; wait", nil, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timeout not enforced: %v", time.Since(start))
	}
}

func TestRunContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, "sleep 30", nil, time.Minute); err == nil {
		t.Fatal("cancelled hook succeeded")
	}
}
