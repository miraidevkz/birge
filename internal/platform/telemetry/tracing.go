package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// instrumentationName is the tracer name of every Birge span.
const instrumentationName = "birge"

// Tracer returns the process tracer. Before Setup installs a provider (and
// always when no OTLP endpoint is configured) it is the OpenTelemetry no-op
// tracer, so instrumented code costs almost nothing.
func Tracer() trace.Tracer { return otel.Tracer(instrumentationName) }

// Options configure Setup.
type Options struct {
	// Service is the service.name resource attribute ("birge-api",
	// "birge-worker"). It is the only resource attribute: host names and
	// process arguments are not sent.
	Service string
	// Enabled is true when an OTLP endpoint is configured
	// (OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT).
	// False keeps the no-op tracer: no exporter, no collector needed.
	Enabled bool
	// Logger receives exporter errors (no span data).
	Logger *slog.Logger
}

// Setup installs the global tracer provider: OTLP over HTTP, configured by
// the standard OTEL_EXPORTER_OTLP_* variables (endpoint, headers, TLS) and
// sampled by OTEL_TRACES_SAMPLER (default: parent-based, always on), with
// the allow-list in front of the exporter. The returned function flushes
// and stops the exporter; call it on shutdown.
func Setup(ctx context.Context, opts Options) (func(context.Context) error, error) {
	if !opts.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlp exporter: %w", err)
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("telemetry export failed", "event", "TelemetryError", "error", err)
	}))
	tp := NewTracerProvider(sdktrace.NewBatchSpanProcessor(exp), opts.Service)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// NewTracerProvider returns a provider whose spans reach next only through
// the allow-list (NewAllowListProcessor). Setup uses it with the OTLP
// batcher; tests use it with an in-memory exporter, so they exercise the
// same filter production does.
func NewTracerProvider(next sdktrace.SpanProcessor, service string) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(NewAllowListProcessor(next)),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))),
	)
}

// traceContext is the W3C trace context propagator; Birge does not
// propagate baggage (it could carry anything).
var traceContext = propagation.TraceContext{}

// TraceParent returns the W3C traceparent of the span in ctx, or "" when
// there is none (tracing off, or work started outside a traced request).
// The outbox stores it in the event envelope.
func TraceParent(ctx context.Context) string {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ""
	}
	carrier := propagation.MapCarrier{}
	traceContext.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// ContextWithTraceParent returns ctx with the remote span context of
// traceParent as parent; an empty or malformed value returns ctx unchanged.
func ContextWithTraceParent(ctx context.Context, traceParent string) context.Context {
	if traceParent == "" {
		return ctx
	}
	return traceContext.Extract(ctx, propagation.MapCarrier{"traceparent": traceParent})
}

// HTTPMethod returns m if it is a standard method and "OTHER" otherwise, so
// a client cannot put its own string into a span or a metric label.
func HTTPMethod(m string) string {
	if m != "OTHER" && httpMethods[m] {
		return m
	}
	return "OTHER"
}

// StartHTTP starts the server span of one request. It is always a new
// root: a traceparent header from the internet is not trusted (it would let
// a client choose trace IDs and sampling). The name is provisional; EndHTTP
// renames the span to the route template once routing is done.
func StartHTTP(ctx context.Context, method string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, "HTTP "+HTTPMethod(method),
		trace.WithNewRoot(), trace.WithSpanKind(trace.SpanKindServer))
}

// EndHTTP names and ends the server span and records the request metrics.
// route must be a route template (the ServeMux pattern), never the path.
func EndHTTP(span trace.Span, method, route string, status int, elapsed time.Duration) {
	span.SetName(route)
	span.SetAttributes(
		attribute.String(KeyHTTPMethod, HTTPMethod(method)),
		attribute.String(KeyHTTPRoute, route),
		attribute.Int(KeyHTTPStatus, status),
	)
	if status >= 500 {
		span.SetStatus(codes.Error, "")
	}
	span.End()
	ObserveHTTP(route, status, elapsed)
}
