package tracing

import (
	"context"
	"testing"
)

// Setup must work with the installed SDK (its default resource may use a
// newer semantic convention schema than ours).
func TestSetupWithExporter(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1") // nothing listens; export is lazy
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	_, span := Tracer().Start(context.Background(), "x")
	span.End()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shutdown(ctx) // export fails (canceled); that's fine
}
