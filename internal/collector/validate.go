package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const defaultTimeout = 30 * time.Second

// ValidationResult captures the real Collector config validation output.
type ValidationResult struct {
	Command string
	Output  string
}

// ValidateConfig runs `otelcol validate --config <path>`.
func ValidateConfig(ctx context.Context, binary, configPath string) (*ValidationResult, error) {
	if binary == "" {
		binary = "otelcol"
	}
	if configPath == "" {
		return nil, fmt.Errorf("collector config path is required")
	}

	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "validate", "--config", configPath)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("otelcol validate timed out after %s", defaultTimeout)
		}
		return nil, fmt.Errorf("otelcol validate failed: %w\n%s", err, combined.String())
	}

	return &ValidationResult{
		Command: fmt.Sprintf("%s validate --config %s", binary, configPath),
		Output:  combined.String(),
	}, nil
}
