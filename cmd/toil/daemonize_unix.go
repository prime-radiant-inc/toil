//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func daemonizeCommand(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
