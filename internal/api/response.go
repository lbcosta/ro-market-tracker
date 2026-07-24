package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("falha ao codificar resposta JSON", "error", err)
	}
}

type errorBody struct {
	Error string `json:"error"`
}

func writeErrorMsg(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// writeUpstreamError traduz um erro vindo do cliente gnjoy para uma resposta
// HTTP apropriada: um 429 do upstream (mesmo depois das tentativas com
// backoff do client) vira 503, para deixar claro que é uma condição
// temporária; qualquer outro status HTTP do upstream vira 502; qualquer
// outro erro (rede, timeout, etc.) também vira 502.
func writeUpstreamError(w http.ResponseWriter, err error) {
	var httpErr *gnjoy.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == http.StatusTooManyRequests {
			slog.Warn("upstream manteve o rate limiting mesmo após as tentativas com backoff")
			writeErrorMsg(w, http.StatusServiceUnavailable, "o servidor de mercado do GnJoy LATAM está limitando a taxa de requisições no momento; tente novamente em instantes")
			return
		}
		slog.Error("upstream retornou status inesperado", "status", httpErr.StatusCode, "body", httpErr.Body)
		writeErrorMsg(w, http.StatusBadGateway, "o servidor de mercado do GnJoy LATAM retornou uma resposta inesperada")
		return
	}
	slog.Error("falha consultando upstream", "error", err)
	writeErrorMsg(w, http.StatusBadGateway, "falha ao consultar o servidor de mercado do GnJoy LATAM")
}
