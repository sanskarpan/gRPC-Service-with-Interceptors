// Package tracing wires OpenTelemetry context propagation and export into the
// gRPC boundary. A disabled exporter still leaves a valid no-op provider.
package tracing

import (
	"context"
	"strings"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/stats"
)

// Init configures the process-wide tracer provider and returns its bounded
// shutdown function. Exporter startup failures are returned to fail readiness.
func Init(ctx context.Context, cfg config.TracingConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}
	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
		otlptracehttp.WithTimeout(5 * time.Second),
	}
	if strings.HasPrefix(strings.ToLower(cfg.Endpoint), "http://") {
		options = append(options, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	provider := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.NewSchemaless(
			semconv.ServiceName(cfg.ServiceName),
		)),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

// NewServerHandler returns the OpenTelemetry gRPC stats handler for server
// entry points and propagation.
func NewServerHandler() stats.Handler {
	return otelgrpc.NewServerHandler()
}

// Start creates a span using the globally configured tracer provider.
func Start(ctx context.Context, name string) (context.Context, oteltrace.Span) {
	return otel.Tracer("grpc-service").Start(ctx, name)
}

// AddEvent annotates the current span when tracing is enabled.
func AddEvent(ctx context.Context, name string) {
	oteltrace.SpanFromContext(ctx).AddEvent(name)
}
