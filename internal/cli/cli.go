package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/andyphied/otel-policy-lab/internal/collector"
	"github.com/andyphied/otel-policy-lab/internal/evaluator"
	"github.com/andyphied/otel-policy-lab/internal/policy"
	"github.com/andyphied/otel-policy-lab/internal/report"
	"github.com/andyphied/otel-policy-lab/internal/runner"
	"github.com/andyphied/otel-policy-lab/internal/telemetry"
)

// Version is set by the main package or linker flags.
var Version = "dev"

const usage = `otel-policy-lab - test OpenTelemetry Collector configs against policy assertions

Usage:
  otel-policy-lab test --collector-config <path> --input <path> --policy <path> [--report <path>] [--runner fixture] [--fail-on-warn] [--validate-collector]
  otel-policy-lab validate --collector-config <path> [--otelcol-bin otelcol]
  otel-policy-lab --version

Exit codes:
  0  all checks passed
  1  policy failure (or warning with --fail-on-warn)
  2  usage or input parsing error
  3  pipeline runner error
`

// Run executes the CLI with the provided arguments.
func Run(args []string) int {
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	switch args[1] {
	case "test":
		return runTest(args[2:])
	case "validate":
		return runValidate(args[2:])
	case "-v", "--version", "version":
		fmt.Println(Version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[1], usage)
		return 2
	}
}

func runTest(args []string) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	collectorConfig := fs.String("collector-config", "", "path to OpenTelemetry Collector config YAML")
	input := fs.String("input", "", "path to OTLP JSON telemetry fixture")
	policyPath := fs.String("policy", "", "path to policy YAML")
	reportPath := fs.String("report", "", "optional path to write JSON report")
	runnerName := fs.String("runner", "fixture", "pipeline runner to use (fixture)")
	failOnWarn := fs.Bool("fail-on-warn", false, "exit non-zero on warnings")
	validateCollector := fs.Bool("validate-collector", false, "run otelcol validate before policy evaluation")
	otelcolBin := fs.String("otelcol-bin", "otelcol", "otelcol binary for collector validation")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *collectorConfig == "" || *input == "" || *policyPath == "" {
		fmt.Fprintln(os.Stderr, "error: --collector-config, --input, and --policy are required")
		fs.Usage()
		return 2
	}

	if *validateCollector {
		if _, err := collector.ValidateConfig(context.Background(), *otelcolBin, *collectorConfig); err != nil {
			fmt.Fprintf(os.Stderr, "error: collector validation failed: %v\n", err)
			return 2
		}
	}

	pol, err := policy.Load(*policyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load policy: %v\n", err)
		return 2
	}

	fixture, err := telemetry.Load(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load fixture: %v\n", err)
		return 2
	}

	r, err := runner.New(*runnerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create runner: %v\n", err)
		return 2
	}

	result, err := r.Run(context.Background(), runner.RunRequest{
		FixturePath:         *input,
		CollectorConfigPath: *collectorConfig,
		Fixture:             fixture,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: run pipeline: %v\n", err)
		return 3
	}

	checks := evaluator.Evaluate(pol, result.Input, result.Output, result.RunnerWarnings)
	summary := buildSummary(result.Input, result.Output)

	rep := report.Build(
		checks,
		report.RunnerMetadata{
			Name:                  result.RunnerName,
			SimulatedProcessors:   result.SimulatedProcessors,
			UnsupportedProcessors: result.UnsupportedProcessors,
		},
		fileMetadata(*input),
		fileMetadata(*policyPath),
		fileMetadata(*collectorConfig),
		summary,
	)

	report.PrintTerminal(rep)

	if *reportPath != "" {
		if err := report.WriteJSON(*reportPath, rep); err != nil {
			fmt.Fprintf(os.Stderr, "error: write report: %v\n", err)
			return 2
		}
	}

	return report.ExitCode(rep, *failOnWarn)
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	collectorConfig := fs.String("collector-config", "", "path to OpenTelemetry Collector config YAML")
	otelcolBin := fs.String("otelcol-bin", "otelcol", "otelcol binary")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *collectorConfig == "" {
		fmt.Fprintln(os.Stderr, "error: --collector-config is required")
		fs.Usage()
		return 2
	}

	result, err := collector.ValidateConfig(context.Background(), *otelcolBin, *collectorConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL collector config validation failed: %v\n", err)
		return 2
	}

	fmt.Printf("PASS collector config validated with %s\n", result.Command)
	if strings.TrimSpace(result.Output) != "" {
		fmt.Print(result.Output)
	}
	return 0
}

func buildSummary(input, output *telemetry.Set) report.SummaryStatistics {
	stats := telemetry.SummaryStats(input, output)
	summary := report.SummaryStatistics{
		InputLogCount:     stats.InputLogCount,
		OutputLogCount:    stats.OutputLogCount,
		InputSpanCount:    stats.InputSpanCount,
		OutputSpanCount:   stats.OutputSpanCount,
		InputMetricCount:  stats.InputMetricCount,
		OutputMetricCount: stats.OutputMetricCount,
		InputSeriesCount:  stats.InputSeriesCount,
		OutputSeriesCount: stats.OutputSeriesCount,
	}
	if stats.InputLogCount > 0 {
		change := float64(stats.OutputLogCount-stats.InputLogCount) / float64(stats.InputLogCount) * 100
		summary.LogVolumeChangePct = change
	}
	return summary
}

func fileMetadata(path string) report.FileMetadata {
	info, err := os.Stat(path)
	if err != nil {
		return report.FileMetadata{Path: path}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return report.FileMetadata{Path: path, Size: info.Size()}
	}
	hash := sha256.Sum256(data)
	return report.FileMetadata{
		Path:   path,
		Size:   info.Size(),
		SHA256: strings.ToLower(hex.EncodeToString(hash[:])),
	}
}
