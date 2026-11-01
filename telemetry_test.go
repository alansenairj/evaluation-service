package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestInitTelemetrySemEndpointNaoInstrumenta(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := initTelemetry(context.Background(), "svc")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestInitTelemetryComEndpointPropagaTraceContext(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:4317")
	shutdown, err := initTelemetry(context.Background(), "svc")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	defer func() {
		// Sem coletor, o flush final falha; o timeout evita travar o teste.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()

	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatal("TracerProvider global nao e o do SDK")
	}
	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if carrier["traceparent"] == "" {
		t.Fatal("traceparent nao injetado: o trace quebraria entre servicos e na fila SQS")
	}
}

func TestLogJSONTemTraceIDQuandoHaSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(traceHandler{slog.NewJSONHandler(&buf, nil)})

	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	logger.InfoContext(ctx, "com span")
	logger.Info("sem span")

	linhas := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	var com, sem map[string]any
	if err := json.Unmarshal(linhas[0], &com); err != nil {
		t.Fatalf("log nao e JSON: %v", err)
	}
	_ = json.Unmarshal(linhas[1], &sem)
	if id, _ := com["trace_id"].(string); len(id) != 32 {
		t.Fatalf("trace_id ausente no log com span: %v", com)
	}
	if _, ok := sem["trace_id"]; ok {
		t.Fatal("log sem span nao deve ter trace_id")
	}
}

func TestAccessLogNaoLogaHealth(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health", nil))
	if buf.Len() != 0 {
		t.Fatalf("/health nao deve gerar log: %s", buf.String())
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/validate?user_id=segredo", nil))
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("nao e JSON: %v", err)
	}
	if m["level"] != "ERROR" || m["status"] != float64(500) || m["path"] != "/validate" {
		t.Fatalf("log de acesso inesperado: %v", m)
	}
	if bytes.Contains(buf.Bytes(), []byte("segredo")) {
		t.Fatal("query string vazou no log")
	}
}
