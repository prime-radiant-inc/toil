//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

func daemonizeCommand(cmd *exec.Cmd) error {
	return fmt.Errorf("daemon mode is not supported on windows")
}
