//go:build windows

package webui

import (
	"fmt"
	"os"
)

func sendRestartSignal(proc *os.Process) error {
	// Windows doesn't support SIGUSR1
	return fmt.Errorf("automatic restart not supported on Windows")
}
