package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// activityEventView é a serialização em JSON de um gnjoy.ActivityEvent para
// a barra de atividades do frontend (ver internal/web/static/activity-bar.js).
// Horários viajam como milissegundos desde a epoch Unix para o JS não
// precisar lidar com parsing de timezone.
type activityEventView struct {
	ID         int64  `json:"id"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"startedAt"`
	DispatchAt int64  `json:"dispatchAt,omitempty"`
	Err        string `json:"error,omitempty"`
}

func toActivityEventView(ev gnjoy.ActivityEvent) activityEventView {
	view := activityEventView{
		ID:        ev.ID,
		Label:     ev.Label,
		Status:    string(ev.Status),
		StartedAt: ev.StartedAt.UnixMilli(),
		Err:       ev.Err,
	}
	if ev.Status == gnjoy.ActivityWaiting && !ev.DispatchAt.IsZero() {
		view.DispatchAt = ev.DispatchAt.UnixMilli()
	}
	return view
}

// ActivityStream trata GET /web/activity/stream: um endpoint Server-Sent
// Events que expõe em tempo real a atividade do gnjoy.Client (fila do rate
// limiter, requisições em voo, sucesso/erro) para a barra de atividades do
// frontend.
//
// Ao conectar, o cliente recebe um evento "snapshot" com o histórico atual
// (mantido em internal/gnjoy.ActivityLog); a partir daí, cada transição de
// estado de uma chamada é enviada como um evento "update". A conexão fica
// aberta até o contexto da requisição encerrar (aba fechada, navegação para
// outra página) — o EventSource do navegador reconecta sozinho se cair.
func (h *Handler) ActivityStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming não suportado", http.StatusInternalServerError)
		return
	}

	log := h.client.Activity()
	updates, unsubscribe := log.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	snapshot := log.Snapshot()
	views := make([]activityEventView, len(snapshot))
	for i, ev := range snapshot {
		views[i] = toActivityEventView(ev)
	}
	if err := writeSSE(w, "snapshot", views); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case ev, ok := <-updates:
			if !ok {
				return
			}
			if err := writeSSE(w, "update", toActivityEventView(ev)); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("web: falha ao codificar evento SSE de atividade", "error", err)
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}
