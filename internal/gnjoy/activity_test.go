package gnjoy

import (
	"errors"
	"testing"
	"time"
)

func testLabels() activityLabels {
	return activityLabels{
		InProgress: "Consultando detalhes do item anunciado",
		Success:    "Detalhes do item consultados",
		Error:      "Falha ao consultar detalhes do item",
	}
}

// TestActivityHandleTrocaOTextoPorFase é o comportamento que existe para a
// barra de atividades não parecer travada: o gerúndio só vale enquanto a
// chamada está em curso; ao terminar, o texto passa a estar conjugado no
// tempo certo.
func TestActivityHandleTrocaOTextoPorFase(t *testing.T) {
	labels := testLabels()

	t.Run("sucesso", func(t *testing.T) {
		log := NewActivityLog(10)
		h := log.begin(labels)
		h.running()
		h.succeed()

		events := log.Snapshot()
		if len(events) != 1 {
			t.Fatalf("len(Snapshot) = %d, quero 1 (as fases atualizam o mesmo evento)", len(events))
		}
		if events[0].Status != ActivitySuccess {
			t.Errorf("Status = %q, quero %q", events[0].Status, ActivitySuccess)
		}
		if events[0].Label != labels.Success {
			t.Errorf("Label = %q, quero %q", events[0].Label, labels.Success)
		}
	})

	t.Run("erro", func(t *testing.T) {
		log := NewActivityLog(10)
		h := log.begin(labels)
		h.fail(errors.New("connection refused"))

		events := log.Snapshot()
		if events[0].Status != ActivityError {
			t.Errorf("Status = %q, quero %q", events[0].Status, ActivityError)
		}
		if events[0].Label != labels.Error {
			t.Errorf("Label = %q, quero %q", events[0].Label, labels.Error)
		}
		if events[0].Err != "connection refused" {
			t.Errorf("Err = %q, quero \"connection refused\"", events[0].Err)
		}
	})

	t.Run("erro nil não quebra", func(t *testing.T) {
		log := NewActivityLog(10)
		log.begin(labels).fail(nil)

		if got := log.Snapshot()[0].Err; got != "" {
			t.Errorf("Err = %q, quero vazio", got)
		}
	})

	t.Run("aguardando e em voo usam o texto em andamento", func(t *testing.T) {
		log := NewActivityLog(10)
		h := log.begin(labels)

		dispatchAt := time.Now().Add(3 * time.Second)
		h.waiting(dispatchAt)
		ev := log.Snapshot()[0]
		if ev.Status != ActivityWaiting {
			t.Errorf("Status = %q, quero %q", ev.Status, ActivityWaiting)
		}
		if ev.Label != labels.InProgress {
			t.Errorf("Label = %q, quero %q", ev.Label, labels.InProgress)
		}
		if !ev.DispatchAt.Equal(dispatchAt) {
			t.Errorf("DispatchAt = %v, quero %v", ev.DispatchAt, dispatchAt)
		}

		h.running()
		ev = log.Snapshot()[0]
		if ev.Status != ActivityRunning {
			t.Errorf("Status = %q, quero %q", ev.Status, ActivityRunning)
		}
		if ev.Label != labels.InProgress {
			t.Errorf("Label = %q, quero %q", ev.Label, labels.InProgress)
		}
		// Em voo não há previsão de término, então não há cronômetro a exibir.
		if !ev.DispatchAt.IsZero() {
			t.Errorf("DispatchAt = %v, quero zero enquanto a requisição está em voo", ev.DispatchAt)
		}
	})
}

func TestActivityLogDescartaOsMaisAntigos(t *testing.T) {
	log := NewActivityLog(3)
	for i := range 5 {
		log.begin(activityLabels{InProgress: string(rune('a' + i))}).succeed()
	}

	events := log.Snapshot()
	if len(events) != 3 {
		t.Fatalf("len(Snapshot) = %d, quero 3 (a capacidade)", len(events))
	}
	// Os três últimos, na ordem em que aconteceram.
	if events[0].ID != 3 || events[1].ID != 4 || events[2].ID != 5 {
		t.Errorf("IDs = %d/%d/%d, quero 3/4/5", events[0].ID, events[1].ID, events[2].ID)
	}
}

func TestActivityLogSnapshotEhCopia(t *testing.T) {
	log := NewActivityLog(10)
	log.begin(testLabels()).succeed()

	snapshot := log.Snapshot()
	snapshot[0].Label = "mexido por fora"

	if got := log.Snapshot()[0].Label; got == "mexido por fora" {
		t.Error("Snapshot devolveu a fatia interna: mexer nela alterou o log")
	}
}

func TestActivityLogSubscribe(t *testing.T) {
	log := NewActivityLog(10)

	updates, unsubscribe := log.Subscribe()

	// Assinantes recebem só o que for publicado depois da assinatura; o que
	// veio antes vem por Snapshot.
	h := log.begin(testLabels())
	h.succeed()

	primeiro := receber(t, updates)
	if primeiro.Status != ActivityWaiting {
		t.Errorf("primeiro evento = %q, quero %q", primeiro.Status, ActivityWaiting)
	}
	segundo := receber(t, updates)
	if segundo.Status != ActivitySuccess {
		t.Errorf("segundo evento = %q, quero %q", segundo.Status, ActivitySuccess)
	}
	if segundo.ID != primeiro.ID {
		t.Errorf("IDs diferentes (%d e %d): as fases da mesma chamada precisam manter o ID", primeiro.ID, segundo.ID)
	}

	unsubscribe()
	log.begin(testLabels()).succeed()

	select {
	case ev, ok := <-updates:
		if ok {
			t.Errorf("recebi %+v depois de cancelar a assinatura", ev)
		}
	default:
	}
}

// TestActivityLogNaoTravaComAssinanteLento é o que impede a barra de
// atividades de virar um gargalo: se ninguém está drenando o canal, a chamada
// real ao upstream não pode ficar bloqueada esperando espaço.
func TestActivityLogNaoTravaComAssinanteLento(t *testing.T) {
	log := NewActivityLog(500)
	_, unsubscribe := log.Subscribe() // assinante que nunca lê
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			log.begin(testLabels()).succeed()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publicar travou por causa de um assinante que não lê o canal")
	}
}

func TestActivityLogVariosAssinantes(t *testing.T) {
	log := NewActivityLog(10)

	a, cancelA := log.Subscribe()
	defer cancelA()
	b, cancelB := log.Subscribe()
	defer cancelB()

	log.begin(testLabels())

	if got := receber(t, a); got.Status != ActivityWaiting {
		t.Errorf("assinante A recebeu %q", got.Status)
	}
	if got := receber(t, b); got.Status != ActivityWaiting {
		t.Errorf("assinante B recebeu %q", got.Status)
	}
}

func TestClientActivityExposto(t *testing.T) {
	c := New()
	if c.Activity() == nil {
		t.Fatal("Client.Activity() = nil, quero o log de atividade")
	}
	if got := len(c.Activity().Snapshot()); got != 0 {
		t.Errorf("um client recém-criado já tem %d eventos, quero 0", got)
	}
}

func receber(t *testing.T, ch <-chan ActivityEvent) ActivityEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("nenhum evento chegou ao assinante")
		return ActivityEvent{}
	}
}
