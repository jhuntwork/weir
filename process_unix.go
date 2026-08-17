//go:build unix

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureProcessTree puts each direct Weir child in its own process group so
// context cancellation cannot leave descendants running after the parent exits.
func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	return killProcessGroup(cmd)
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// A negative PID targets the child-created process group rather than only
	// the direct child. ESRCH means the group has already exited.
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
