package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

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

// suspensionView é a serialização da suspensão por 429 (ver
// internal/gnjoy/suspend.go) para o frontend. Vai sempre com o estado
// completo, nunca como diferença: uma aba que perdesse o evento de liberação
// ficaria com o aviso na tela para sempre.
type suspensionView struct {
	Suspended bool  `json:"suspended"`
	Since     int64 `json:"since,omitempty"`
}

func toSuspensionView(s gnjoy.Suspension) suspensionView {
	view := suspensionView{Suspended: s.Suspended}
	if s.Suspended {
		view.Since = s.Since.UnixMilli()
	}
	return view
}

// sseKeepaliveInterval é de quanto em quanto tempo um comentário SSE é enviado
// numa conexão parada. Um stream que fica minutos em silêncio — o que é o
// normal aqui, já que só há evento quando o programa fala com o site — é
// candidato a ser fechado por proxy ou antivírus.
const sseKeepaliveInterval = 25 * time.Second

// ActivityStream trata GET /web/activity/stream: um endpoint Server-Sent
// Events que expõe em tempo real a atividade do gnjoy.Client (fila do rate
// limiter, requisições em voo, sucesso/erro) para a barra de atividades do
// frontend, e o estado de suspensão por 429 para o aviso no topo da página.
//
// Ao conectar, o cliente recebe um evento "snapshot" com o histórico atual
// (mantido em internal/gnjoy.ActivityLog) e um "suspension" com o estado
// corrente; a partir daí, cada transição é enviada como "update" ou
// "suspension". A conexão fica aberta até o contexto da requisição encerrar
// (aba fechada, navegação para outra página) — o EventSource do navegador
// reconecta sozinho se cair.
//
// As duas fontes são multiplexadas em UM select, e não numa goroutine cada:
// dois escritores no mesmo ResponseWriter intercalariam os Flush e
// corromperiam o enquadramento dos eventos.
func (h *Handler) ActivityStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming não suportado", http.StatusInternalServerError)
		return
	}

	log := h.client.Activity()
	updates, unsubscribe := log.Subscribe()
	defer unsubscribe()

	// Assinar antes de ler o estado, nas duas fontes: na ordem inversa, uma
	// transição ocorrida entre a leitura e a assinatura se perderia. Repetir
	// um evento é inofensivo, porque os dois payloads são estado completo.
	suspensao := h.client.Suspension()
	suspensoes, unsubscribeSuspensao := suspensao.Subscribe()
	defer unsubscribeSuspensao()

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
	if err := writeSSE(w, "suspension", toSuspensionView(suspensao.Current())); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

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
		case s, ok := <-suspensoes:
			if !ok {
				return
			}
			if err := writeSSE(w, "suspension", toSuspensionView(s)); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
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
