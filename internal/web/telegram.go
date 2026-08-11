package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// notifyTelegramRequest é o corpo esperado por NotifyTelegram. O texto já
// vem pronto do navegador (ver notifyTelegram em watchlist.js) — é lá que
// se sabe o item, o preço, a loja e o /navi da linha que disparou o aviso;
// o servidor só repassa para o Telegram, sem entender nada de watchlist.
type notifyTelegramRequest struct {
	Text string `json:"text"`
}

// NotifyTelegram trata POST /web/watchlist/notify-telegram: repassa o texto
// recebido para o bot configurado em telegram.txt (ver telegramClientFromFile,
// em cmd/server/main.go).
//
// Sem configuração (telegramClient nil) é um no-op silencioso: o navegador
// chama este endpoint todo santo acerto da watchlist, sem saber se o
// Telegram foi configurado ou não — a maioria de quem baixa a release nunca
// mexe nisso.
//
// Falha no envio (token inválido, sem internet etc.) só vira log: o toast e
// a notificação nativa do navegador já avisaram o usuário de qualquer
// forma (ver notifyHit em watchlist.js), então não há nada de acionável
// para mostrar na tela por causa disto.
func (h *Handler) NotifyTelegram(w http.ResponseWriter, r *http.Request) {
	if h.telegramClient == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var req notifyTelegramRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "'text' é obrigatório", http.StatusBadRequest)
		return
	}

	if err := h.telegramClient.Send(r.Context(), req.Text); err != nil {
		slog.Error("web: falha ao notificar o Telegram", "error", err)
		http.Error(w, "não foi possível notificar o Telegram", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
