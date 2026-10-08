//go:build darwin || linux

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func processSupported() bool { return true }
func openNonblocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func terminateProcessGroup(cmd *exec.Cmd) error { return signalProcessGroup(cmd, syscall.SIGTERM) }
func killProcessGroup(cmd *exec.Cmd) error      { return signalProcessGroup(cmd, syscall.SIGKILL) }
func signalProcessGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
