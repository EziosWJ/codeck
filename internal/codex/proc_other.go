//go:build !unix

package codex

import (
	"context"
	"os/exec"
)

// configureProcess is a no-op where process groups are not available.
func configureProcess(cmd *exec.Cmd) {}

// watchForCancel relies on exec.CommandContext's own cancellation on platforms
// without process groups.
func watchForCancel(ctx context.Context, cmd *exec.Cmd) func() {
	return func() {}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
