package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

type EvaluationResponse struct {
	FlagName string `json:"flag_name"`
	UserID   string `json:"user_id"`
	Result   bool   `json:"result"`
}

func (a *App) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *App) evaluationHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Parsear os query parameters
	userID := r.URL.Query().Get("user_id")
	flagName := r.URL.Query().Get("flag_name")

	if userID == "" || flagName == "" {
		http.Error(w, `{"error": "user_id e flag_name são obrigatórios"}`, http.StatusBadRequest)
		return
	}

	// 2. Obter a decisão (lógica de cache/serviço está em evaluator.go)
	result, err := a.getDecision(r.Context(), userID, flagName)
	if err != nil {
		// Se o erro for "não encontrado", retornamos 'false' (comportamento seguro)
		if _, ok := err.(*NotFoundError); ok {
			result = false
		} else {
			// Outros erros (serviços offline, etc)
			slog.ErrorContext(r.Context(), "erro ao avaliar flag", "flag", flagName, "error", err)
			http.Error(w, `{"error": "Erro interno ao avaliar a flag"}`, http.StatusBadGateway)
			return
		}
	}

	// 3. Enviar evento para SQS (assincronamente)
	// Isso não bloqueia a resposta para o cliente.
	// WithoutCancel: a requisicao termina antes do envio, mas o span pai segue valido.
	go a.sendEvaluationEvent(context.WithoutCancel(r.Context()), userID, flagName, result)

	// 4. Retornar a resposta
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(EvaluationResponse{
		FlagName: flagName,
		UserID:   userID,
		Result:   result,
	})
}
