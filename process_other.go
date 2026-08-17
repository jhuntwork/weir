//go:build !unix

package main

import (
	"errors"
	"os"
	"os/exec"
)

// Non-Unix platforms retain direct-child termination until an equivalent
// process-tree primitive is implemented for that platform.
func configureProcessTree(_ *exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
