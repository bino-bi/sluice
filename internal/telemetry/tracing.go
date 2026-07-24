// SPDX-License-Identifier: AGPL-3.0-or-later

package telemetry

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// initTracing builds the OTLP exporter and a batching TracerProvider,
// installs it as the otel global (with W3C tracecontext + baggage
// propagation), and returns its shutdown. Span attributes must never
// carry raw SQL or secret bytes — fingerprints and error codes only.
func initTracing(ctx context.Context, svc ServiceInfo, cfg TracingConfig) (func(context.Context) error, error) {
	var (
		exp *otlptrace.Exporter
		err error
	)
	switch cfg.Protocol {
	case "", "grpc":
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exp, err = otlptracegrpc.New(ctx, opts...)
	case "http":
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exp, err = otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("telemetry: unknown tracing protocol %q", cfg.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: otlp exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(svc.Name),
		semconv.ServiceVersion(svc.Version),
		semconv.ServiceInstanceID(svc.Instance),
	))
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// HTTPMiddleware wraps h in an otelhttp server span named
// "<method> <route pattern>" (falling back to the operation name when no
// informative pattern matched). Wrap the mux directly — a WithContext
// clone between this handler and the mux hides Request.Pattern from the
// renaming pass. Callers gate on their tracing flag so disabled
// deployments pay no wrapping cost.
func HTTPMiddleware(operation string) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return otelhttp.NewHandler(h, operation,
			otelhttp.WithSpanNameFormatter(func(op string, r *http.Request) string {
				// ServeMux patterns already carry the method
				// ("GET /v1/health"); a bare "/" adds nothing over op.
				if r.Pattern != "" && r.Pattern != "/" {
					return r.Pattern
				}
				return op
			}),
		)
	}
}
