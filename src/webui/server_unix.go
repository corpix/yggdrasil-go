//go:build !windows

package webui

import (
	"os"
	"syscall"
)

func sendRestartSignal(proc *os.Process) error {
	return proc.Signal(syscall.SIGUSR1)
}
