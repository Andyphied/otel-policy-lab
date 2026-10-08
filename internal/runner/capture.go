package runner

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/andyphied/otel-policy-lab/internal/telemetry"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

const (
	maxMessageBytes    = 16 << 20
	maxCaptureBytes    = 64 << 20
	maxCaptureItems    = 100000
	maxCaptureRequests = 10000
)

type captureSink struct {
	mu                     sync.Mutex
	raw                    *telemetry.OTLP
	bytes, items, requests int
	failure                string
	failed                 chan struct{}
	server                 *grpc.Server
	listener               net.Listener
	served                 chan struct{}
}

func startCapture() (*captureSink, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("cannot start loopback capture listener")
	}
	sink := &captureSink{
		raw:    &telemetry.OTLP{Logs: plog.NewLogs(), Traces: ptrace.NewTraces(), Metrics: pmetric.NewMetrics()},
		failed: make(chan struct{}), listener: listener, served: make(chan struct{}),
	}
	sink.server = grpc.NewServer(grpc.MaxRecvMsgSize(maxMessageBytes), grpc.MaxConcurrentStreams(8), grpc.StatsHandler(sink))
	plogotlp.RegisterGRPCServer(sink.server, &logCapture{sink: sink})
	ptraceotlp.RegisterGRPCServer(sink.server, &traceCapture{sink: sink})
	pmetricotlp.RegisterGRPCServer(sink.server, &metricCapture{sink: sink})
	go func() {
		defer close(sink.served)
		if err := sink.server.Serve(listener); err != nil {
			sink.mu.Lock()
			sink.failLocked("capture_server")
			sink.mu.Unlock()
		}
	}()
	return sink, nil
}

func (s *captureSink) failLocked(category string) {
	if s.failure == "" {
		s.failure = category
		close(s.failed)
	}
}

func (s *captureSink) reserve(size, items int) error {
	if s.failure != "" {
		return status.Error(codes.ResourceExhausted, "capture unavailable")
	}
	if size > maxMessageBytes || s.bytes+size > maxCaptureBytes || s.items+items > maxCaptureItems || s.requests+1 > maxCaptureRequests {
		s.failLocked("capture_limit")
		return status.Error(codes.ResourceExhausted, "capture limit exceeded")
	}
	s.bytes += size
	s.items += items
	s.requests++
	return nil
}

func (s *captureSink) failureCategory() string { s.mu.Lock(); defer s.mu.Unlock(); return s.failure }

// Close only after the Collector exits, so batch shutdown exports are retained.
func (s *captureSink) close() {
	done := make(chan struct{})
	go func() { s.server.GracefulStop(); close(done) }()
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		s.mu.Lock()
		s.failLocked("capture_shutdown")
		s.mu.Unlock()
		s.server.Stop()
		<-done
	}
	<-s.served
}

func (s *captureSink) normalized() *telemetry.Set {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.raw.Normalize()
	telemetry.Canonicalize(set)
	return set
}

func (s *captureSink) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (s *captureSink) HandleRPC(_ context.Context, stat stats.RPCStats) {
	if end, ok := stat.(*stats.End); ok && end.Error != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		category := "capture_transport"
		if status.Code(end.Error) == codes.ResourceExhausted {
			category = "capture_limit"
		}
		s.failLocked(category)
	}
}
func (s *captureSink) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (s *captureSink) HandleConn(context.Context, stats.ConnStats)                       {}

type logCapture struct {
	plogotlp.UnimplementedGRPCServer
	sink *captureSink
}

func (c *logCapture) Export(_ context.Context, req plogotlp.ExportRequest) (plogotlp.ExportResponse, error) {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	data := req.Logs()
	if err := c.sink.reserve((&plog.ProtoMarshaler{}).LogsSize(data), data.LogRecordCount()); err != nil {
		return plogotlp.NewExportResponse(), err
	}
	data.ResourceLogs().MoveAndAppendTo(c.sink.raw.Logs.ResourceLogs())
	return plogotlp.NewExportResponse(), nil
}

type traceCapture struct {
	ptraceotlp.UnimplementedGRPCServer
	sink *captureSink
}

func (c *traceCapture) Export(_ context.Context, req ptraceotlp.ExportRequest) (ptraceotlp.ExportResponse, error) {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	data := req.Traces()
	if err := c.sink.reserve((&ptrace.ProtoMarshaler{}).TracesSize(data), data.SpanCount()); err != nil {
		return ptraceotlp.NewExportResponse(), err
	}
	data.ResourceSpans().MoveAndAppendTo(c.sink.raw.Traces.ResourceSpans())
	return ptraceotlp.NewExportResponse(), nil
}

type metricCapture struct {
	pmetricotlp.UnimplementedGRPCServer
	sink *captureSink
}

func (c *metricCapture) Export(_ context.Context, req pmetricotlp.ExportRequest) (pmetricotlp.ExportResponse, error) {
	c.sink.mu.Lock()
	defer c.sink.mu.Unlock()
	data := req.Metrics()
	if err := c.sink.reserve((&pmetric.ProtoMarshaler{}).MetricsSize(data), data.MetricCount()+data.DataPointCount()); err != nil {
		return pmetricotlp.NewExportResponse(), err
	}
	data.ResourceMetrics().MoveAndAppendTo(c.sink.raw.Metrics.ResourceMetrics())
	return pmetricotlp.NewExportResponse(), nil
}

func submitFixture(ctx context.Context, conn *grpc.ClientConn, raw *telemetry.OTLP) error {
	if raw.Logs.LogRecordCount() > 0 {
		response, err := plogotlp.NewGRPCClient(conn).Export(ctx, plogotlp.NewExportRequestFromLogs(raw.Logs))
		if err != nil {
			return fmt.Errorf("OTLP logs submission failed; acceptance is uncertain")
		}
		if response.PartialSuccess().RejectedLogRecords() != 0 || response.PartialSuccess().ErrorMessage() != "" {
			return fmt.Errorf("OTLP logs submission returned partial success")
		}
	}
	if raw.Traces.SpanCount() > 0 {
		response, err := ptraceotlp.NewGRPCClient(conn).Export(ctx, ptraceotlp.NewExportRequestFromTraces(raw.Traces))
		if err != nil {
			return fmt.Errorf("OTLP traces submission failed; acceptance is uncertain")
		}
		if response.PartialSuccess().RejectedSpans() != 0 || response.PartialSuccess().ErrorMessage() != "" {
			return fmt.Errorf("OTLP traces submission returned partial success")
		}
	}
	if raw.Metrics.MetricCount() > 0 {
		response, err := pmetricotlp.NewGRPCClient(conn).Export(ctx, pmetricotlp.NewExportRequestFromMetrics(raw.Metrics))
		if err != nil {
			return fmt.Errorf("OTLP metrics submission failed; acceptance is uncertain")
		}
		if response.PartialSuccess().RejectedDataPoints() != 0 || response.PartialSuccess().ErrorMessage() != "" {
			return fmt.Errorf("OTLP metrics submission returned partial success")
		}
	}
	return nil
}

func validateFixtureSize(raw *telemetry.OTLP) error {
	if (&plog.ProtoMarshaler{}).LogsSize(raw.Logs) > maxMessageBytes || (&ptrace.ProtoMarshaler{}).TracesSize(raw.Traces) > maxMessageBytes || (&pmetric.ProtoMarshaler{}).MetricsSize(raw.Metrics) > maxMessageBytes {
		return fmt.Errorf("fixture exceeds the 16 MiB per-signal OTLP message limit")
	}
	return nil
}
