//go:build unix

package codex

import (
	"context"
	"os/exec"
	"syscall"
)

// configureProcess places the child in its own process group so that the whole
// tree can be signalled at once.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// watchForCancel kills the child's process group when ctx is done and returns a
// function that stops watching. Codex spawns shell children; signalling only
// the direct child would leave those running.
func watchForCancel(ctx context.Context, cmd *exec.Cmd) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessGroup(cmd)
		case <-done:
		}
	}()
	return func() { close(done) }
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative pid targets the whole process group.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
