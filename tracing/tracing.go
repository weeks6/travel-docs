// Package tracing sets up OpenTelemetry tracing. Spans are exported over
// OTLP/HTTP when OTEL_EXPORTER_OTLP_ENDPOINT (or the traces-specific
// variable) is set; otherwise tracing is a no-op.
package tracing

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentation = "github.com/weeks6/travel-docs"

// Tracer returns the application's tracer.
func Tracer() trace.Tracer { return otel.Tracer(instrumentation) }

// Setup installs the global tracer provider and propagator. The returned
// function flushes and stops the exporter.
func Setup(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}
	// Schemaless, so it merges with the SDK's default resource whatever
	// semantic convention version that uses.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Middleware starts a server span for every request, continuing a trace
// from an incoming traceparent header.
func Middleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		r := c.Request()
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		route := c.Path()
		if route == "" {
			route = "unmatched"
		}
		ctx, span := Tracer().Start(ctx, r.Method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.HTTPRoute(route),
				semconv.URLPath(r.URL.Path),
			))
		defer span.End()
		c.SetRequest(r.WithContext(ctx))

		err := next(c)

		if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
			span.SetAttributes(semconv.HTTPResponseStatusCode(resp.Status))
			if resp.Status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(resp.Status))
			}
		}
		if err != nil {
			span.RecordError(err)
		}
		return err
	}
}

// LogAttrs returns trace_id and span_id attributes for ctx's span, if any.
func LogAttrs(ctx context.Context) []any {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return []any{slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String())}
}

// Attr is a shorthand for a string span attribute.
func Attr(k, v string) attribute.KeyValue { return attribute.String(k, v) }
