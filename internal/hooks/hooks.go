// Package hooks runs the user's on-success and on-failure commands.
package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DefaultTimeout bounds a hook.
const DefaultTimeout = 5 * time.Minute

// Run executes command with "sh -c", adding env to the environment, and
// returns its combined output. The whole process group is killed when the
// timeout (or ctx) ends.
func Run(ctx context.Context, command string, env []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("hook timed out after %s", timeout)
	}
	return out.String(), err
}
