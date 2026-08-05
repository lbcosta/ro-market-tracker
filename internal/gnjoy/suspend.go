package gnjoy

import (
	"context"
	"errors"
	"sync"
	"time"
)

// A calmaria global (ver extendCooldown) resolve o 429 passageiro: ela segura
// as chamadas pelo tempo que o próprio site pediu e a vida segue. O que ela
// não resolve é o 429 que significa "sua cota acabou": aí o site vai recusar
// tudo por um tempo que ele não informa, e continuar mandando requisições — e
// deixar o usuário continuar pedindo — só afunda mais o buraco.
//
// A suspensão é essa segunda camada. Ao primeiro 429 ela fecha a porta: as
// chamadas seguintes falham na saída, sem tocar no site. Uma sonda periódica
// (ver internal/web/suspension.go) é a única que continua passando, e é a
// resposta dela que reabre a porta.
//
// É opcional de propósito (WithSuspendOn429). Sem a option, o Client se
// comporta como sempre — insistindo com backoff —, que é o que faz sentido
// para quem o usa como biblioteca, e é o que os testes de retry exercitam.

// ErrSuspended é devolvido, sem que nenhuma requisição saia, enquanto as
// consultas estão suspensas por um 429 recente.
var ErrSuspended = errors.New("gnjoy: o site limitou as consultas (429) e elas estão suspensas até ele voltar a responder")

// Suspension é o estado publicado aos assinantes. Vai sempre completo, nunca
// como diferença: um assinante que perdesse o evento de liberação ficaria
// preso no estado anterior para sempre.
type Suspension struct {
	Suspended bool
	Since     time.Time
}

// SuspensionState guarda se as consultas estão suspensas e avisa quem
// acompanha. É seguro para uso concorrente.
type SuspensionState struct {
	mu    sync.Mutex
	since time.Time

	// gen conta as suspensões: cada nova incrementa. É o que impede uma
	// resposta que já estava em voo de desfazer uma suspensão mais nova que
	// ela — ver release.
	gen uint64

	subs map[chan Suspension]struct{}
}

func newSuspensionState() *SuspensionState {
	return &SuspensionState{subs: make(map[chan Suspension]struct{})}
}

// Current devolve o estado atual. É o que o frontend pede ao conectar, antes
// de haver qualquer transição para observar.
func (s *SuspensionState) Current() Suspension {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *SuspensionState) snapshotLocked() Suspension {
	return Suspension{Suspended: !s.since.IsZero(), Since: s.since}
}

// Subscribe devolve um canal com as mudanças de estado e a função que cancela
// a assinatura.
//
// O canal tem capacidade 1 e coalesce: um assinante lento perde os estados
// intermediários, nunca o último. É diferente do ActivityLog, que descarta o
// evento quando o assinante não acompanha — lá a próxima atualização corrige o
// atraso, e aqui perder a liberação deixaria o aviso na tela para sempre.
func (s *SuspensionState) Subscribe() (<-chan Suspension, func()) {
	ch := make(chan Suspension, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
}

// publishLocked entrega o estado atual a todos os assinantes. Precisa ser
// chamada com o mutex preso: é ele que serializa os publicadores e garante que
// o último a escrever é o último a ser lido.
func (s *SuspensionState) publishLocked() {
	ev := s.snapshotLocked()
	for ch := range s.subs {
		// Esvazia antes de escrever: o que estava na fila é mais velho que
		// este, e o assinante só precisa do mais recente.
		select {
		case <-ch:
		default:
		}
		ch <- ev
	}
}

// admit decide se uma chamada pode sair e devolve a geração de suspensão que
// ela observou — o release depois usa esse número para não desfazer uma
// suspensão que tenha começado no meio do caminho.
//
// bypass é para a sonda de recuperação, a única chamada que precisa atravessar
// a porta fechada.
func (s *SuspensionState) admit(bypass bool) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.since.IsZero() && !bypass {
		return s.gen, ErrSuspended
	}
	return s.gen, nil
}

// suspend fecha a porta. Repetir enquanto já suspenso não republica nada: o
// estado (inclusive o "desde quando") continua o da primeira vez.
func (s *SuspensionState) suspend(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.since.IsZero() {
		return
	}
	s.since = now
	s.gen++
	s.publishLocked()
}

// release reabre a porta, se e somente se nenhuma suspensão nova tiver
// começado desde que a chamada foi admitida.
//
// A guarda importa mais do que parece: uma requisição admitida antes do 429
// pode responder 200 depois dele — ela saiu quando o site ainda respondia — e
// sem a comparação de geração essa resposta velha desfaria uma suspensão
// legítima e recém-criada.
func (s *SuspensionState) release(observedGen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.since.IsZero() || s.gen != observedGen {
		return
	}
	s.since = time.Time{}
	s.publishLocked()
}

// Suspension devolve o estado de suspensão do Client — se as consultas estão
// suspensas por um 429 e um jeito de assinar as mudanças. Usado pelo frontend
// (ver internal/web/suspension.go e activity.go).
//
// O estado existe mesmo sem WithSuspendOn429; o que a option muda é se um 429
// passa a alimentá-lo. Assim quem observa não precisa de dois caminhos.
func (c *Client) Suspension() *SuspensionState {
	return c.suspension
}

// ProbeUpstream gasta uma requisição mínima para descobrir se o site voltou a
// responder, e reabre as consultas se voltou. É a única chamada que atravessa
// a suspensão — por isso é um método, e não uma CallOption pública: uma opção
// exportada seria uma chave-mestra na porta que acabou de ser instalada, e
// qualquer chamada futura poderia desligar a proteção sem que nada aqui
// percebesse.
//
// Quem decide o ritmo é quem chama (ver internal/web/suspension.go). A
// liberação em si não acontece aqui: acontece em do, ao receber qualquer
// resposta que não seja 429 — é ela a prova de que o site voltou.
func (c *Client) ProbeUpstream(ctx context.Context) error {
	cfg := callConfig{maxRetries: 0, allowWhileSuspended: true}
	return c.pingUpstream(ctx, cfg, activityLabels{
		InProgress: "Verificando se o site voltou a aceitar consultas",
		Success:    "O site voltou a aceitar consultas",
		Error:      "O site ainda está limitando as consultas",
	})
}
