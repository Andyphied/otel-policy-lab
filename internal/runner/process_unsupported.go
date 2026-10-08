//go:build !darwin && !linux

package runner

import (
	"fmt"
	"os"
	"os/exec"
)

func processSupported() bool                        { return false }
func openNonblocking(path string) (*os.File, error) { return os.Open(path) }
func configureProcess(*exec.Cmd) error {
	return fmt.Errorf("real Collector runner requires macOS or Linux process-group support")
}
func terminateProcessGroup(*exec.Cmd) error { return fmt.Errorf("process groups are unsupported") }
func killProcessGroup(*exec.Cmd) error      { return fmt.Errorf("process groups are unsupported") }
