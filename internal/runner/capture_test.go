package runner

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

func newTestSink() *captureSink {
	return &captureSink{raw: &telemetry.OTLP{Logs: plog.NewLogs(), Traces: ptrace.NewTraces(), Metrics: pmetric.NewMetrics()}, failed: make(chan struct{})}
}

func TestCaptureLimitsAreExplicitAndSticky(t *testing.T) {
	for _, dimension := range []string{"bytes", "items", "requests"} {
		t.Run(dimension, func(t *testing.T) {
			sink := newTestSink()
			switch dimension {
			case "bytes":
				sink.bytes = maxCaptureBytes
			case "items":
				sink.items = maxCaptureItems
			case "requests":
				sink.requests = maxCaptureRequests
			}
			err := sink.reserve(1, 1)
			if status.Code(err) != codes.ResourceExhausted || sink.failureCategory() != "capture_limit" {
				t.Fatalf("limit silently accepted: %v", err)
			}
			select {
			case <-sink.failed:
			default:
				t.Fatal("failure did not notify the run")
			}
			if err := sink.reserve(0, 0); err == nil {
				t.Fatal("failed capture resumed accepting data")
			}
		})
	}
}

func TestCaptureTransportRejectionMarksRunFailed(t *testing.T) {
	sink := newTestSink()
	sink.HandleRPC(context.Background(), &stats.End{Error: status.Error(codes.ResourceExhausted, "private raw details")})
	if sink.failureCategory() != "capture_limit" {
		t.Fatalf("lost pre-handler rejection: %s", sink.failureCategory())
	}
}

func TestCaptureAppendsAcrossRequestsAndKeepsTypedData(t *testing.T) {
	sink := newTestSink()
	server := &logCapture{sink: sink}
	for _, body := range []string{"b", "a"} {
		data := plog.NewLogs()
		resource := data.ResourceLogs().AppendEmpty()
		resource.Resource().Attributes().PutInt("number", 9223372036854775807)
		log := resource.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		log.Body().SetStr(body)
		log.Attributes().PutEmptyMap("nested").PutBool("flag", true)
		if _, err := server.Export(context.Background(), plogotlp.NewExportRequestFromLogs(data)); err != nil {
			t.Fatal(err)
		}
	}
	if sink.raw.Logs.LogRecordCount() != 2 {
		t.Fatal("capture lost a request")
	}
	value, _ := sink.raw.Logs.ResourceLogs().At(0).Resource().Attributes().Get("number")
	if value.Int() != 9223372036854775807 {
		t.Fatal("capture coerced integer precision")
	}
	if got := sink.normalized(); len(got.Logs) != 2 || got.Logs[0].Body != "a" || got.Logs[1].Body != "b" {
		t.Fatal("capture normalization was not canonical")
	}
}

type partialLogServer struct {
	plogotlp.UnimplementedGRPCServer
	calls atomic.Int32
}

func (s *partialLogServer) Export(_ context.Context, _ plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	s.calls.Add(1)
	response := plogotlp.NewExportResponse()
	response.PartialSuccess().SetRejectedLogRecords(1)
	response.PartialSuccess().SetErrorMessage("private-rejection-secret")
	return response, nil
}

func TestSubmissionRejectsPartialSuccessWithoutRetryOrPrivateText(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	partial := &partialLogServer{}
	plogotlp.RegisterGRPCServer(server, partial)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := connectReceiver(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	raw := emptyRaw(t)
	err = submitFixture(ctx, conn, raw)
	if err == nil || !strings.Contains(err.Error(), "partial success") || strings.Contains(err.Error(), "private-rejection-secret") {
		t.Fatalf("unsafe or missing partial-success error: %v", err)
	}
	if partial.calls.Load() != 1 {
		t.Fatalf("retried ambiguous submission %d times", partial.calls.Load())
	}
}

func TestExportFailureMarkerSurvivesTruncationAndWriteBoundaries(t *testing.T) {
	buffer := &privateBuffer{}
	_, _ = buffer.Write([]byte("Exporting fa"))
	_, _ = buffer.Write([]byte("iled. private-secret"))
	_, _ = buffer.Write([]byte(strings.Repeat("x", maxDiagnosticBytes*2)))
	if !buffer.hasExportFailure() {
		t.Fatal("asynchronous export failure was forgotten")
	}
	if len(buffer.text()) > maxDiagnosticBytes || strings.Contains(buffer.text(), "private-secret") {
		t.Fatal("buffer is not bounded")
	}
}

func TestReadinessMarkerSurvivesTruncationAndWriteBoundaries(t *testing.T) {
	buffer := &privateBuffer{}
	_, _ = buffer.Write([]byte("Everything is rea"))
	_, _ = buffer.Write([]byte("dy. Begin running and processing data."))
	_, _ = buffer.Write([]byte(strings.Repeat("x", maxDiagnosticBytes*2)))
	if !buffer.isReady() {
		t.Fatal("Collector readiness was forgotten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitCollectorReadiness(ctx, &privateBuffer{}); err == nil {
		t.Fatal("missing readiness should not pass")
	}
}

func TestHarnessSelectsOnlyPopulatedSignals(t *testing.T) {
	source := topologySource + "    traces/unused: {receivers: [otlp/source], processors: [batch/unused], exporters: [otlp/production]}\n"
	top, err := parseTopology([]byte(source), emptyRaw(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(top.pipelines) != 1 || top.pipelines[0].Signal != "logs" || mappingValue(top.processors, "batch/unused") != nil {
		t.Fatal("unpopulated signal was included in executed pipeline provenance")
	}
}
