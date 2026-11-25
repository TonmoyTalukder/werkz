//go:build unix

package tools

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the command in its own process group and makes cancellation
// (timeout, Esc) kill the whole group, not just the shell.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
}

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
