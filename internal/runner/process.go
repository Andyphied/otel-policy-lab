package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxDiagnosticBytes = 64 << 10
	shutdownAllowance  = 3 * time.Second
	cleanupAllowance   = time.Second
)

func openRegular(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input must be a regular file")
	}
	f, err := openNonblocking(path)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("input must be a regular file")
	}
	return f, nil
}

// Collector output can include telemetry or resolved secrets. It never crosses
// this private, bounded buffer; callers receive fixed categories only.
type privateBuffer struct {
	mu            sync.Mutex
	data          []byte
	exportFailure bool
	ready         bool
}

func (b *privateBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	// Include the previous suffix so a marker split across writes is retained.
	previous := b.data
	if len(previous) > 32 {
		previous = previous[len(previous)-32:]
	}
	prefix := p
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	if exportFailureText(string(p)) || exportFailureText(string(previous)+string(prefix)) {
		b.exportFailure = true
	}
	if strings.Contains(strings.ToLower(string(p)), "everything is ready") || strings.Contains(strings.ToLower(string(previous)+string(prefix)), "everything is ready") {
		b.ready = true
	}
	if n >= maxDiagnosticBytes {
		b.data = append(b.data[:0], p[n-maxDiagnosticBytes:]...)
	} else {
		if extra := len(b.data) + n - maxDiagnosticBytes; extra > 0 {
			b.data = b.data[extra:]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *privateBuffer) hasExportFailure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exportFailure
}
func (b *privateBuffer) isReady() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.ready }

func waitCollectorReadiness(ctx context.Context, logs *privateBuffer) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if logs.isReady() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Collector did not emit the standard lifecycle readiness marker")
		case <-ticker.C:
		}
	}
}
func exportFailureText(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "exporting failed") || strings.Contains(text, "exporter failed") || strings.Contains(text, "failed to export") || strings.Contains(text, "failed to shutdown")
}
func (b *privateBuffer) text() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }

type childProcess struct {
	cmd    *exec.Cmd
	logs   *privateBuffer
	done   chan struct{}
	err    error
	forced bool
}

func startProcess(binary, dir string, args ...string) (*childProcess, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 100 * time.Millisecond
	if err := configureProcess(cmd); err != nil {
		return nil, err
	}
	logs := &privateBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start the supplied Collector executable")
	}
	p := &childProcess{cmd: cmd, logs: logs, done: make(chan struct{})}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}

func (p *childProcess) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// stop retains the capture service until graceful termination has finished.
// Descendants in the process group are killed even if the leader exits early.
func (p *childProcess) stop(ctx context.Context, graceful bool) error {
	defer func() { _ = killProcessGroup(p.cmd) }()
	if p.exited() {
		return nil
	}
	if graceful {
		if err := terminateProcessGroup(p.cmd); err != nil {
			return fmt.Errorf("cannot signal Collector process group")
		}
		timer := time.NewTimer(shutdownAllowance)
		defer timer.Stop()
		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	p.forced = true
	if err := killProcessGroup(p.cmd); err != nil {
		return fmt.Errorf("cannot terminate Collector process group")
	}
	timer := time.NewTimer(cleanupAllowance)
	defer timer.Stop()
	select {
	case <-p.done:
		if graceful {
			return fmt.Errorf("Collector required forced termination during shutdown")
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("Collector process could not be reaped within the cleanup deadline")
	}
}

func (p *childProcess) record(d *Diagnostics) {
	if p.forced {
		d.ExitStatus = "forced"
	} else if p.exited() {
		d.ExitStatus = "exited"
	} else {
		d.ExitStatus = "running"
	}
	if !p.exited() || p.cmd.ProcessState == nil {
		return
	}
	code := p.cmd.ProcessState.ExitCode()
	d.ExitCode = &code
	if code < 0 && !p.forced {
		d.ExitStatus = "signaled"
	}
}

func processFailure(logs string) string {
	lower := strings.ToLower(logs)
	switch {
	case strings.Contains(lower, "address already in use"):
		return "bind_conflict"
	case strings.Contains(lower, "unknown type"), strings.Contains(lower, "unknown processor"), strings.Contains(lower, "unknown exporter"), strings.Contains(lower, "unknown receiver"):
		return "missing_component"
	case strings.Contains(lower, "failed to get config"), strings.Contains(lower, "error decoding"), strings.Contains(lower, "invalid configuration"), strings.Contains(lower, "failed to build"), strings.Contains(lower, "invalid keys"), strings.Contains(lower, "configuration references"):
		return "invalid_config"
	default:
		return "process_exit"
	}
}

var versionPattern = regexp.MustCompile(`(?m)(?:^|[[:space:]])v?([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4})(?:[[:space:]]|$)`)

var unknownComponentPattern = regexp.MustCompile(`unknown type:\s*"([^"\r\n]+)"\s+for id:\s*"([^"\r\n]+)"`)

func missingComponentHint(logs string, topology *topology) string {
	for _, match := range unknownComponentPattern.FindAllStringSubmatch(logs, -1) {
		id := match[2]
		if !componentID.MatchString(id) || strings.SplitN(id, "/", 2)[0] != match[1] {
			continue
		}
		if id == "otlp/policy_lab" {
			return "; required harness component " + id + " is unavailable"
		}
		if mappingValue(topology.processors, id) != nil {
			return "; processor component " + id + " is unavailable"
		}
	}
	return ""
}

func collectorVersion(ctx context.Context, binary, dir string) (string, error) {
	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	p, err := startProcess(binary, dir, "--version")
	if err != nil {
		return "", err
	}
	select {
	case <-p.done:
		_ = p.stop(versionCtx, false)
		if p.err != nil {
			return "", fmt.Errorf("Collector version query failed")
		}
		match := versionPattern.FindStringSubmatch(p.logs.text())
		if len(match) == 2 {
			return match[1], nil
		}
		return "", fmt.Errorf("Collector version was unavailable")
	case <-versionCtx.Done():
		if err := p.stop(versionCtx, false); err != nil {
			return "", err
		}
		return "", fmt.Errorf("Collector version query timed out")
	}
}
