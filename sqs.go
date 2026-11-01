package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sqs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Evento que será enviado para a fila
type EvaluationEvent struct {
	UserID    string    `json:"user_id"`
	FlagName  string    `json:"flag_name"`
	Result    bool      `json:"result"`
	Timestamp time.Time `json:"timestamp"`
}

// sendEvaluationEvent envia um evento para a fila SQS
func (a *App) sendEvaluationEvent(ctx context.Context, userID, flagName string, result bool) {
	// Se a URL da fila não foi configurada, apenas loga localmente e sai.
	if a.SqsSvc == nil || a.SqsQueueURL == "" {
		slog.InfoContext(ctx, "SQS desabilitado, evento descartado", "flag", flagName, "result", result)
		return
	}

	event := EvaluationEvent{
		UserID:    userID,
		FlagName:  flagName,
		Result:    result,
		Timestamp: time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		slog.ErrorContext(ctx, "erro ao serializar evento SQS", "error", err)
		return
	}

	// Span de producer + traceparent no atributo da mensagem: o analytics continua o trace.
	ctx, span := otel.Tracer("evaluation-service").Start(ctx, "sqs.send", trace.WithSpanKind(trace.SpanKindProducer))
	defer span.End()
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	attrs := map[string]*sqs.MessageAttributeValue{}
	for k, v := range carrier {
		attrs[k] = &sqs.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}

	// Envia a mensagem
	_, err = a.SqsSvc.SendMessageWithContext(ctx, &sqs.SendMessageInput{
		MessageBody:       aws.String(string(body)),
		QueueUrl:          aws.String(a.SqsQueueURL),
		MessageAttributes: attrs,
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.ErrorContext(ctx, "erro ao enviar mensagem para SQS", "error", err)
	} else {
		slog.InfoContext(ctx, "evento de avaliação enviado para SQS", "flag", flagName)
	}
}
