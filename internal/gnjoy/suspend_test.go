package gnjoy_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// newSuspendingClient monta um client que suspende ao primeiro 429, com rate
// limit alto para o teste não gastar tempo na fila.
func newSuspendingClient(t *testing.T) (*gnjoy.Client, *gnjoytest.Server) {
	t.Helper()
	mock := gnjoytest.New(gnjoytest.DemoConfig())
	t.Cleanup(mock.Close)

	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
		gnjoy.WithSuspendOn429(),
	)
	return client, mock
}

func buscarEspada(ctx context.Context, c *gnjoy.Client) error {
	_, err := c.SearchShops(ctx, gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada",
	})
	return err
}

// TestPrimeiro429SuspendeAsChamadasSeguintes é o contrato desta feature: ao
// primeiro 429, a próxima chamada nem chega a sair. Insistir contra um site que
// acabou de dizer "pare" é o que transforma um bloqueio curto em longo.
func TestPrimeiro429SuspendeAsChamadasSeguintes(t *testing.T) {
	client, mock := newSuspendingClient(t)

	// Retry-After alto: se a suspensão não cortasse, a chamada ficaria
	// esperando a calmaria em vez de falhar de imediato.
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "60"}, 1)

	if err := buscarEspada(context.Background(), client); err == nil {
		t.Fatal("a primeira chamada deveria falhar com o 429")
	}
	if !client.Suspension().Current().Suspended {
		t.Fatal("o 429 deveria ter suspendido as consultas")
	}

	mock.ResetRequests()
	err := buscarEspada(context.Background(), client)
	if !errors.Is(err, gnjoy.ErrSuspended) {
		t.Errorf("erro = %v, quero ErrSuspended", err)
	}
	if got := mock.RequestCount(); got != 0 {
		t.Errorf("%d requisições chegaram ao site, quero 0: a chamada não pode sair", got)
	}
}

// TestSuspensaoAbortaORetryEmVoo: o retry acontece dentro de uma chamada só, e
// a suspensão precisa cortá-lo também — senão a chamada que TOMOU o 429
// continuaria insistindo cinco vezes contra o bloqueio que ela mesma acabou de
// descobrir.
func TestSuspensaoAbortaORetryEmVoo(t *testing.T) {
	client, mock := newSuspendingClient(t)

	// Muito mais falhas do que tentativas: se o retry rodasse, a contagem
	// subiria junto.
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 20)

	mock.ResetRequests()
	if err := buscarEspada(context.Background(), client); err == nil {
		t.Fatal("a chamada deveria falhar")
	}
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("%d requisições chegaram ao site, quero 1: o retry deveria ter sido cortado", got)
	}
}

// TestSemAOptionO429ContinuaSendoRepetido guarda o comportamento de quem usa o
// pacote como biblioteca: sem a option, nada muda — o Client insiste com
// backoff como sempre insistiu.
func TestSemAOptionO429ContinuaSendoRepetido(t *testing.T) {
	mock := gnjoytest.New(gnjoytest.DemoConfig())
	t.Cleanup(mock.Close)
	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)

	mock.ResetRequests()
	if err := buscarEspada(context.Background(), client); err != nil {
		t.Fatalf("a busca deveria se recuperar pelo retry: %v", err)
	}
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("%d requisições, quero 2 (a que levou 429 e a que deu certo)", got)
	}
	if client.Suspension().Current().Suspended {
		t.Error("sem a option, um 429 não pode suspender nada")
	}
}

// TestSondaLiberaQuandoOSiteVolta: a sonda é a única chamada que atravessa a
// porta fechada, e é a resposta dela que reabre.
func TestSondaLiberaQuandoOSiteVolta(t *testing.T) {
	client, mock := newSuspendingClient(t)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	_ = buscarEspada(context.Background(), client)
	if !client.Suspension().Current().Suspended {
		t.Fatal("quero as consultas suspensas antes de sondar")
	}

	if err := client.ProbeUpstream(context.Background()); err != nil {
		t.Fatalf("a sonda deveria passar com o site respondendo: %v", err)
	}
	if client.Suspension().Current().Suspended {
		t.Error("o site respondeu: as consultas deveriam ter sido liberadas")
	}
	if err := buscarEspada(context.Background(), client); err != nil {
		t.Errorf("a busca deveria voltar a funcionar: %v", err)
	}
}

// TestSondaNaoLiberaEnquantoOSiteRecusa: uma sonda que também toma 429 mantém
// tudo suspenso — e, por ser ela mesma um 429, não pode se auto-liberar.
func TestSondaNaoLiberaEnquantoOSiteRecusa(t *testing.T) {
	client, mock := newSuspendingClient(t)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 2)
	_ = buscarEspada(context.Background(), client)

	if err := client.ProbeUpstream(context.Background()); err == nil {
		t.Fatal("a sonda deveria falhar com o site ainda recusando")
	}
	if !client.Suspension().Current().Suspended {
		t.Error("o site ainda recusa: as consultas precisam continuar suspensas")
	}
}

// TestRespostaEmVooNaoLiberaSuspensaoNova é a corrida que a contagem de gerações
// existe para matar: uma requisição que saiu ANTES do 429 pode responder 200
// depois dele. Essa resposta é verdadeira sobre o passado e não pode desfazer
// uma suspensão mais nova que ela.
func TestRespostaEmVooNaoLiberaSuspensaoNova(t *testing.T) {
	client, mock := newSuspendingClient(t)

	// A busca sai e fica segurada; enquanto isso, outra chamada toma o 429 e
	// suspende. A resposta boa chega depois.
	mock.QueueFailure(gnjoytest.Failure{Passthrough: true, Delay: 300 * time.Millisecond}, 1)

	done := make(chan error, 1)
	go func() { done <- buscarEspada(context.Background(), client) }()

	time.Sleep(50 * time.Millisecond)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	_, _ = client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{SvrId: 303, MapId: 835, SSI: "s-primordial-129"})
	if !client.Suspension().Current().Suspended {
		t.Fatal("quero as consultas suspensas pelo 429")
	}

	if err := <-done; err != nil {
		t.Fatalf("a busca atrasada deveria ter dado certo: %v", err)
	}
	if !client.Suspension().Current().Suspended {
		t.Error("uma resposta que saiu antes do 429 não pode desfazer a suspensão")
	}
}

// TestSuspensaoPublicaEstadoParaAssinantes: é por este canal que o aviso chega
// à tela. Perder o evento de liberação deixaria o aviso preso para sempre, por
// isso o canal coalesce em vez de descartar.
func TestSuspensaoPublicaEstadoParaAssinantes(t *testing.T) {
	client, mock := newSuspendingClient(t)

	updates, unsubscribe := client.Suspension().Subscribe()
	defer unsubscribe()

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	_ = buscarEspada(context.Background(), client)

	select {
	case s := <-updates:
		if !s.Suspended {
			t.Error("quero o estado suspenso")
		}
		if s.Since.IsZero() {
			t.Error("quero saber desde quando")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nenhum evento de suspensão chegou")
	}

	if err := client.ProbeUpstream(context.Background()); err != nil {
		t.Fatalf("sonda: %v", err)
	}
	select {
	case s := <-updates:
		if s.Suspended {
			t.Error("quero o estado liberado")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a liberação não chegou aos assinantes")
	}
}

// TestChamadaEstacionadaNaCalmariaNaoDispara: sem a reconferência dentro da
// espera, uma chamada parada numa calmaria de dois minutos dispararia assim que
// ela acabasse, mesmo já suspensa — que é exatamente o tráfego represado que a
// suspensão existe para impedir.
func TestChamadaEstacionadaNaCalmariaNaoDispara(t *testing.T) {
	client, mock := newSuspendingClient(t)

	// Uma calmaria curta, e a suspensão junto: quando a calmaria terminar, a
	// chamada precisa desistir em vez de sair.
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "1"}, 1)
	_ = buscarEspada(context.Background(), client)

	mock.ResetRequests()
	inicio := time.Now()
	err := buscarEspada(context.Background(), client)
	if !errors.Is(err, gnjoy.ErrSuspended) {
		t.Errorf("erro = %v, quero ErrSuspended", err)
	}
	if elapsed := time.Since(inicio); elapsed > 500*time.Millisecond {
		t.Errorf("a chamada esperou %v: deveria ser recusada na porta, sem entrar na fila", elapsed)
	}
	if got := mock.RequestCount(); got != 0 {
		t.Errorf("%d requisições saíram, quero 0", got)
	}
}
