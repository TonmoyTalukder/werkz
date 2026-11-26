//go:build windows

package mcp

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
