package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// sseEvent é um evento lido do stream: o nome depois de "event:" e o JSON
// depois de "data:".
type sseEvent struct {
	Name string
	Data string
}

// openActivityStream abre o stream e devolve um canal com os eventos que
// forem chegando. O stream é fechado quando o teste termina.
func openActivityStream(t *testing.T, srv *httptest.Server) <-chan sseEvent {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/web/activity/stream", nil)
	if err != nil {
		t.Fatalf("montando requisição: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("abrindo o stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q, quero text/event-stream", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("cache-control = %q, quero no-cache (um stream não pode ser cacheado)", got)
	}

	events := make(chan sseEvent, 64)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		var current sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				current.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.Data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if current.Name != "" {
					events <- current
					current = sseEvent{}
				}
			}
		}
	}()
	return events
}

func nextEvent(t *testing.T, events <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("o stream fechou antes de mandar o evento esperado")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("nenhum evento chegou pelo stream")
		return sseEvent{}
	}
}

// TestActivityStreamMandaSnapshotAoConectar garante que quem abre a página no
// meio do caminho já vê o histórico, em vez de uma barra vazia até a próxima
// chamada acontecer.
func TestActivityStreamMandaSnapshotAoConectar(t *testing.T) {
	srv, _ := newWebServer(t)

	// Gera atividade ANTES de abrir o stream.
	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")

	events := openActivityStream(t, srv)

	ev := nextEvent(t, events)
	if ev.Name != "snapshot" {
		t.Fatalf("primeiro evento = %q, quero \"snapshot\"", ev.Name)
	}

	var snapshot []activityEventView
	if err := json.Unmarshal([]byte(ev.Data), &snapshot); err != nil {
		t.Fatalf("snapshot inválido: %s", ev.Data)
	}
	if len(snapshot) == 0 {
		t.Fatal("snapshot vazio, quero a busca que já aconteceu")
	}
	last := snapshot[len(snapshot)-1]
	if last.Status != string(gnjoy.ActivitySuccess) {
		t.Errorf("status = %q, quero %q", last.Status, gnjoy.ActivitySuccess)
	}
	if !strings.Contains(last.Label, "Espada") {
		t.Errorf("label = %q, quero que mencione o termo buscado", last.Label)
	}
	if last.StartedAt == 0 {
		t.Error("StartedAt = 0, quero o horário em milissegundos")
	}
}

// TestActivityStreamMandaAtualizacoes é o coração da barra: cada fase de cada
// chamada precisa chegar ao navegador em tempo real.
func TestActivityStreamMandaAtualizacoes(t *testing.T) {
	srv, _ := newWebServer(t)

	events := openActivityStream(t, srv)
	if ev := nextEvent(t, events); ev.Name != "snapshot" {
		t.Fatalf("primeiro evento = %q, quero \"snapshot\"", ev.Name)
	}
	if ev := nextEvent(t, events); ev.Name != "suspension" {
		t.Fatalf("segundo evento = %q, quero \"suspension\"", ev.Name)
	}

	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Poring")

	// A mesma chamada é publicada várias vezes conforme avança; o que importa
	// é que o ciclo termine em sucesso, mantendo o mesmo ID.
	var (
		primeiroID int64
		concluiu   bool
	)
	for range 10 {
		ev := nextEvent(t, events)
		if ev.Name != "update" {
			t.Fatalf("evento = %q, quero \"update\"", ev.Name)
		}
		var view activityEventView
		if err := json.Unmarshal([]byte(ev.Data), &view); err != nil {
			t.Fatalf("update inválido: %s", ev.Data)
		}
		if primeiroID == 0 {
			primeiroID = view.ID
		}
		if view.ID != primeiroID {
			t.Fatalf("ID = %d, quero %d (as fases da mesma chamada mantêm o ID)", view.ID, primeiroID)
		}
		if view.Status == string(gnjoy.ActivitySuccess) {
			if !strings.Contains(view.Label, "concluída") {
				t.Errorf("label de sucesso = %q, quero uma frase já concluída", view.Label)
			}
			concluiu = true
			break
		}
		// Enquanto não terminou, o texto tem de estar no gerúndio.
		if !strings.Contains(view.Label, "Buscando") {
			t.Errorf("label em andamento = %q, quero o texto no gerúndio", view.Label)
		}
	}
	if !concluiu {
		t.Error("a chamada nunca chegou ao status de sucesso")
	}
}

func TestActivityStreamPublicaFalhas(t *testing.T) {
	srv, mock := newWebServer(t)

	events := openActivityStream(t, srv)
	nextEvent(t, events) // snapshot
	nextEvent(t, events) // estado de suspensão inicial

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 20)
	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")

	var viuErro bool
	for range 20 {
		ev := nextEvent(t, events)
		var view activityEventView
		if err := json.Unmarshal([]byte(ev.Data), &view); err != nil {
			t.Fatalf("update inválido: %s", ev.Data)
		}
		if view.Status == string(gnjoy.ActivityError) {
			if !strings.Contains(view.Label, "Falha") {
				t.Errorf("label de erro = %q, quero uma frase de falha", view.Label)
			}
			if view.Err == "" {
				t.Error("Err vazio, quero a mensagem técnica do erro")
			}
			viuErro = true
			break
		}
	}
	if !viuErro {
		t.Error("nenhum evento de erro chegou pelo stream")
	}
}

// TestActivityStreamSemSuporteAStreaming cobre a saída limpa quando o
// ResponseWriter não sabe fazer flush: sem isso, os eventos ficariam presos no
// buffer e o cliente nunca veria nada.
func TestActivityStreamSemSuporteAStreaming(t *testing.T) {
	mock := gnjoytest.New(gnjoytest.DemoConfig())
	defer mock.Close()
	h := NewHandler(gnjoy.New(gnjoy.WithBaseURL(mock.URL)), "test")

	req := httptest.NewRequest(http.MethodGet, "/web/activity/stream", nil)
	rec := httptest.NewRecorder()

	h.ActivityStream(semFlush{rec}, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, quero 500", rec.Code)
	}
}

// semFlush embrulha um ResponseWriter escondendo o http.Flusher.
type semFlush struct{ http.ResponseWriter }

func TestToActivityEventView(t *testing.T) {
	inicio := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	disparo := inicio.Add(3 * time.Second)

	t.Run("aguardando leva o horário de disparo", func(t *testing.T) {
		view := toActivityEventView(gnjoy.ActivityEvent{
			ID: 7, Label: "Buscando algo", Status: gnjoy.ActivityWaiting,
			StartedAt: inicio, DispatchAt: disparo,
		})
		if view.DispatchAt != disparo.UnixMilli() {
			t.Errorf("DispatchAt = %d, quero %d", view.DispatchAt, disparo.UnixMilli())
		}
		if view.StartedAt != inicio.UnixMilli() {
			t.Errorf("StartedAt = %d, quero %d", view.StartedAt, inicio.UnixMilli())
		}
	})

	t.Run("em voo não tem previsão de disparo", func(t *testing.T) {
		view := toActivityEventView(gnjoy.ActivityEvent{
			ID: 7, Status: gnjoy.ActivityRunning, StartedAt: inicio, DispatchAt: disparo,
		})
		// Só faz sentido mostrar cronômetro enquanto a requisição espera na
		// fila; depois de disparada, não há previsão de término.
		if view.DispatchAt != 0 {
			t.Errorf("DispatchAt = %d, quero 0", view.DispatchAt)
		}
	})

	t.Run("aguardando sem horário definido", func(t *testing.T) {
		view := toActivityEventView(gnjoy.ActivityEvent{
			ID: 7, Status: gnjoy.ActivityWaiting, StartedAt: inicio,
		})
		if view.DispatchAt != 0 {
			t.Errorf("DispatchAt = %d, quero 0", view.DispatchAt)
		}
	})

	t.Run("erro carrega a mensagem", func(t *testing.T) {
		view := toActivityEventView(gnjoy.ActivityEvent{
			ID: 7, Status: gnjoy.ActivityError, StartedAt: inicio, Err: "connection refused",
		})
		if view.Err != "connection refused" {
			t.Errorf("Err = %q, quero \"connection refused\"", view.Err)
		}
		if view.Status != "error" {
			t.Errorf("Status = %q, quero \"error\"", view.Status)
		}
	})
}
