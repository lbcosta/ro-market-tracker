package gnjoy

import (
	"sync"
	"time"
)

// ActivityStatus é o estado de uma chamada ao upstream do GnJoy LATAM, do
// ponto de vista de quem está observando a atividade do Client (a barra de
// atividades do frontend, ver internal/web/activity.go).
type ActivityStatus string

const (
	// ActivityWaiting cobre tanto "na fila do rate limiter, aguardando
	// vaga" quanto "aguardando para tentar de novo depois de um 429" — em
	// ambos os casos a requisição ainda não foi (re)disparada, e DispatchAt
	// indica quando isso deve acontecer.
	ActivityWaiting ActivityStatus = "waiting"
	// ActivityRunning é a requisição já disparada, aguardando a resposta do
	// upstream (duração desconhecida, por isso não tem DispatchAt).
	ActivityRunning ActivityStatus = "running"
	// ActivitySuccess e ActivityError são os estados finais de uma chamada.
	ActivitySuccess ActivityStatus = "success"
	ActivityError   ActivityStatus = "error"
)

// ActivityEvent descreve o estado (em um dado momento) de uma chamada ao
// upstream. O mesmo ID é republicado várias vezes conforme a chamada avança
// (waiting -> running -> success/error), até chegar num estado final.
type ActivityEvent struct {
	ID        int64
	Label     string
	Status    ActivityStatus
	StartedAt time.Time
	// DispatchAt só é relevante quando Status == ActivityWaiting: é o
	// horário em que essa requisição deve ser (re)disparada, usado pelo
	// frontend para renderizar o cronômetro regressivo.
	DispatchAt time.Time
	// Err é a mensagem de erro, presente só quando Status == ActivityError.
	Err string
}

// ActivityLog mantém um histórico limitado das chamadas mais recentes de um
// Client e permite assinar as atualizações em tempo real. É seguro para uso
// concorrente.
type ActivityLog struct {
	mu     sync.Mutex
	cap    int
	nextID int64
	events []ActivityEvent
	subs   map[chan ActivityEvent]struct{}
}

// NewActivityLog cria um ActivityLog vazio, mantendo no máximo capacity
// eventos no histórico (os mais antigos são descartados conforme novos
// chegam).
func NewActivityLog(capacity int) *ActivityLog {
	return &ActivityLog{cap: capacity, subs: make(map[chan ActivityEvent]struct{})}
}

// Snapshot devolve uma cópia do histórico atual, do mais antigo para o mais
// recente.
func (l *ActivityLog) Snapshot() []ActivityEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ActivityEvent, len(l.events))
	copy(out, l.events)
	return out
}

// Subscribe registra um canal que recebe cada atualização publicada a
// partir de agora (não inclui o histórico — para isso, veja Snapshot). A
// função devolvida deve ser chamada para liberar o canal quando o
// assinante não precisar mais dele (ex.: cliente SSE desconectou).
func (l *ActivityLog) Subscribe() (<-chan ActivityEvent, func()) {
	ch := make(chan ActivityEvent, 32)
	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()

	unsubscribe := func() {
		l.mu.Lock()
		delete(l.subs, ch)
		l.mu.Unlock()
	}
	return ch, unsubscribe
}

// publish grava (ou atualiza, se já existir um evento com o mesmo ID) o
// evento no histórico e notifica os assinantes atuais. Assinantes lentos
// (canal cheio) simplesmente perdem essa atualização em vez de bloquear a
// chamada real ao upstream — a próxima atualização (mais recente) ainda
// chega, e uma reconexão SSE corrige o estado via Snapshot.
func (l *ActivityLog) publish(ev ActivityEvent) {
	l.mu.Lock()
	idx := -1
	for i := range l.events {
		if l.events[i].ID == ev.ID {
			idx = i
			break
		}
	}
	if idx >= 0 {
		l.events[idx] = ev
	} else {
		l.events = append(l.events, ev)
		if len(l.events) > l.cap {
			l.events = l.events[len(l.events)-l.cap:]
		}
	}
	subs := make([]chan ActivityEvent, 0, len(l.subs))
	for ch := range l.subs {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// activityLabels são os textos amigáveis de uma chamada, um para cada fase
// do seu ciclo de vida — cada um já na conjugação certa para o momento em
// que aparece, para não soar como "ainda em andamento" depois de resolvida
// (ex.: "Consultando detalhes..." enquanto roda, "Detalhes consultados"
// quando termina com sucesso).
type activityLabels struct {
	// InProgress é usado enquanto a chamada aguarda vaga no rate limiter
	// ou está em voo aguardando resposta (status waiting/running).
	InProgress string
	// Success é usado quando a chamada termina com sucesso.
	Success string
	// Error é usado quando todas as tentativas se esgotam sem sucesso.
	Error string
}

func (l *ActivityLog) begin(labels activityLabels) *activityHandle {
	l.mu.Lock()
	l.nextID++
	id := l.nextID
	l.mu.Unlock()

	startedAt := time.Now()
	l.publish(ActivityEvent{ID: id, Label: labels.InProgress, Status: ActivityWaiting, StartedAt: startedAt})
	return &activityHandle{log: l, id: id, labels: labels, startedAt: startedAt}
}

// activityHandle acompanha uma chamada específica ao longo de seu ciclo de
// vida (fila do rate limiter -> em voo -> sucesso/erro), publicando cada
// transição no ActivityLog.
type activityHandle struct {
	log       *ActivityLog
	id        int64
	labels    activityLabels
	startedAt time.Time
}

func (h *activityHandle) waiting(dispatchAt time.Time) {
	h.log.publish(ActivityEvent{
		ID: h.id, Label: h.labels.InProgress, Status: ActivityWaiting,
		StartedAt: h.startedAt, DispatchAt: dispatchAt,
	})
}

func (h *activityHandle) running() {
	h.log.publish(ActivityEvent{
		ID: h.id, Label: h.labels.InProgress, Status: ActivityRunning, StartedAt: h.startedAt,
	})
}

func (h *activityHandle) succeed() {
	h.log.publish(ActivityEvent{
		ID: h.id, Label: h.labels.Success, Status: ActivitySuccess, StartedAt: h.startedAt,
	})
}

func (h *activityHandle) fail(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	h.log.publish(ActivityEvent{
		ID: h.id, Label: h.labels.Error, Status: ActivityError, StartedAt: h.startedAt, Err: msg,
	})
}
