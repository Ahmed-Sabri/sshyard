//go:build windows

package acp

import "os/exec"

func setGroup(cmd *exec.Cmd) {}

func killProc(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
