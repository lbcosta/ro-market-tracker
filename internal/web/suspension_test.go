package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// A configuração que o binário de fato envia é opt-in (WithSuspendOn429), então
// os testes de retry do pacote gnjoy — que dependem de o retry acontecer —
// exercitam o client SEM ela. Estes aqui cobrem o outro lado: o frontend ligado
// ao client como ele roda de verdade.
func newWebServerSuspendendo(t *testing.T) (*httptest.Server, *gnjoytest.Server, *gnjoy.Client) {
	t.Helper()

	mock := gnjoytest.New(gnjoytest.DemoConfig())
	t.Cleanup(mock.Close)

	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
		gnjoy.WithSuspendOn429(),
	)

	mux := http.NewServeMux()
	RegisterRoutes(mux, client)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, mock, client
}

// TestSearchNaoVaiAoUpstreamQuandoSuspenso: com o site limitando as consultas,
// a busca não pode sair. É o que impede a página de continuar gastando cota
// enquanto o usuário insiste no botão.
func TestSearchNaoVaiAoUpstreamQuandoSuspenso(t *testing.T) {
	srv, mock, _ := newWebServerSuspendendo(t)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada Primordial"}}
	getHTML(t, srv, "/web/search?"+q.Encode())

	mock.ResetRequests()
	// Termo diferente para escapar do cache de busca: o que se quer medir é o
	// que sai para o site, não o que o cache serve.
	q.Set("item", "Selo de Loki")
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	if got := mock.RequestCount(); got != 0 {
		t.Errorf("%d requisições saíram, quero 0 com as consultas suspensas", got)
	}
	wantContains(t, html, "As consultas estão suspensas")
	// "Tente de novo em instantes" seria um mau conselho aqui: tentar de novo
	// é justamente o que não se deve fazer.
	if strings.Contains(html, "Tente novamente em instantes") {
		t.Errorf("a mensagem genérica não serve para a suspensão:\n%s", html)
	}
}

// TestVarreduraNaoSaiQuandoSuspenso: a varredura é o maior consumidor de cota
// do programa, e é justamente ela que tende a causar a suspensão. Começar uma
// já suspenso seria disparar dezenas de chamadas contra uma porta fechada.
func TestVarreduraNaoSaiQuandoSuspenso(t *testing.T) {
	srv, mock, _ := newWebServerSuspendendo(t)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Poring")

	mock.ResetRequests()
	buscarComVarredura(t, srv, "Espada Primordial", true, true)
	if got := mock.RequestCount(); got != 0 {
		t.Errorf("%d requisições saíram, quero 0", got)
	}
}

// TestActivityStreamMandaOEstadoDeSuspensao: uma aba aberta depois do bloqueio
// precisa saber dele — sem o estado no início da conexão, ela mostraria a
// página normal enquanto nada funciona.
func TestActivityStreamMandaOEstadoDeSuspensao(t *testing.T) {
	srv, mock, _ := newWebServerSuspendendo(t)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")

	events := openActivityStream(t, srv)
	if ev := nextEvent(t, events); ev.Name != "snapshot" {
		t.Fatalf("primeiro evento = %q, quero \"snapshot\"", ev.Name)
	}

	ev := nextEvent(t, events)
	if ev.Name != "suspension" {
		t.Fatalf("segundo evento = %q, quero \"suspension\"", ev.Name)
	}
	var view suspensionView
	if err := json.Unmarshal([]byte(ev.Data), &view); err != nil {
		t.Fatalf("payload inválido: %s", ev.Data)
	}
	if !view.Suspended {
		t.Error("quero o estado suspenso")
	}
	if view.Since == 0 {
		t.Error("quero saber desde quando, para a tela poder dizer")
	}
}

// TestSondaLiberaSozinha cobre o ciclo inteiro como ele roda no binário: a
// suspensão acontece, a sonda periódica descobre que o site voltou, e tudo é
// liberado sem ninguém apertar nada.
func TestSondaLiberaSozinha(t *testing.T) {
	srv, mock, client := newWebServerSuspendendo(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartSuspensionProbe(ctx, client, 50*time.Millisecond)

	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)
	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !client.Suspension().Current().Suspended {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("a sonda deveria ter liberado as consultas sozinha")
}
