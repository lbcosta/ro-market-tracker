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

// paginaDeDesafio imita o que o Cloudflare devolve no lugar do conteúdo: um
// HTML de verificação, com status 403.
const paginaDeDesafio = `<!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title>` +
	`<script src="https://challenges.cloudflare.com/turnstile/v0/api.js"></script></head>` +
	`<body>Performing security verification</body></html>`

// newClientDeDesafio é o client de suspensão com a calmaria do desafio
// encurtada: sem isso, o teste da recuperação passaria um minuto de relógio
// esperando algo cujo valor exato não é o que está sendo verificado.
func newClientDeDesafio(t *testing.T) (*gnjoy.Client, *gnjoytest.Server) {
	t.Helper()
	mock := gnjoytest.New(gnjoytest.DemoConfig())
	t.Cleanup(mock.Close)

	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
		gnjoy.WithSuspendOn429(),
		gnjoy.WithCooldownDeDesafio(10*time.Millisecond),
	)
	return client, mock
}

func desafio() gnjoytest.Failure {
	return gnjoytest.Failure{Status: http.StatusForbidden, Body: paginaDeDesafio}
}

// TestDesafioSuspendeAsConsultas é o contrato desta correção. Antes dela o
// desafio caía no caminho de "qualquer status que não é 429 significa que o
// site está atendendo": nenhuma calmaria era registrada, a suspensão era
// LIBERADA, e a próxima requisição saía um segundo depois para levar outro
// desafio — o programa martelava o bloqueio, que é o que o mantém ligado.
func TestDesafioSuspendeAsConsultas(t *testing.T) {
	client, mock := newSuspendingClient(t)
	mock.QueueFailure(desafio(), 1)

	err := buscarEspada(context.Background(), client)
	if !errors.Is(err, gnjoy.ErrDesafioDeNavegador) {
		t.Fatalf("erro = %v, quero que envolva ErrDesafioDeNavegador", err)
	}

	atual := client.Suspension().Current()
	if !atual.Suspended {
		t.Fatal("as consultas não foram suspensas após o desafio")
	}
	if atual.Reason != gnjoy.SuspensaoPorDesafio {
		t.Errorf("motivo = %q, quero %q", atual.Reason, gnjoy.SuspensaoPorDesafio)
	}

	// E a porta fica fechada: a chamada seguinte nem sai.
	mock.ResetRequests()
	if err := buscarEspada(context.Background(), client); !errors.Is(err, gnjoy.ErrSuspended) {
		t.Errorf("segunda chamada devolveu %v, quero ErrSuspended", err)
	}
	if n := mock.RequestCount(); n != 0 {
		t.Errorf("requisições depois da suspensão = %d, quero 0", n)
	}
}

// Um 403 comum não é um bloqueio antibot, e suspender o programa inteiro por
// causa dele seria pior que o problema: é por isso que o reconhecimento olha o
// conteúdo, e não só o status.
func TestForbiddenComumNaoSuspende(t *testing.T) {
	client, mock := newSuspendingClient(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusForbidden, Body: "acesso negado"}, 1)

	err := buscarEspada(context.Background(), client)
	if err == nil {
		t.Fatal("a chamada deveria ter falhado")
	}
	if errors.Is(err, gnjoy.ErrDesafioDeNavegador) {
		t.Errorf("erro = %v, um 403 sem marcas de desafio não deveria virar desafio", err)
	}
	if client.Suspension().Current().Suspended {
		t.Error("as consultas foram suspensas por um 403 comum")
	}
}

// O desafio não pode REABRIR uma suspensão. Era exatamente isso que o release
// fazia: um 429 fechava a porta e o desafio seguinte a escancarava.
func TestDesafioNaoLiberaSuspensaoExistente(t *testing.T) {
	client, mock := newClientDeDesafio(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests}, 1)

	if err := buscarEspada(context.Background(), client); err == nil {
		t.Fatal("a chamada com 429 deveria ter falhado")
	}
	if !client.Suspension().Current().Suspended {
		t.Fatal("o 429 não suspendeu")
	}

	// A sonda atravessa a suspensão; é por ela que o desafio chega.
	mock.QueueFailure(desafio(), 1)
	if err := client.ProbeUpstream(context.Background()); err == nil {
		t.Fatal("a sonda deveria ter falhado contra o desafio")
	}

	atual := client.Suspension().Current()
	if !atual.Suspended {
		t.Fatal("a suspensão foi liberada por um desafio")
	}
	// E o motivo é promovido: um 429 passa sozinho, um desafio não, e é o
	// segundo que o usuário precisa saber.
	if atual.Reason != gnjoy.SuspensaoPorDesafio {
		t.Errorf("motivo = %q, quero %q", atual.Reason, gnjoy.SuspensaoPorDesafio)
	}
}

// Quando o site volta a atender, a sonda reabre — inclusive depois de um
// desafio.
func TestSondaLiberaDepoisDoDesafio(t *testing.T) {
	client, mock := newClientDeDesafio(t)
	mock.QueueFailure(desafio(), 1)

	if err := buscarEspada(context.Background(), client); err == nil {
		t.Fatal("a chamada deveria ter falhado")
	}
	if !client.Suspension().Current().Suspended {
		t.Fatal("o desafio não suspendeu")
	}

	if err := client.ProbeUpstream(context.Background()); err != nil {
		t.Fatalf("sonda contra um site que voltou: %v", err)
	}
	if atual := client.Suspension().Current(); atual.Suspended {
		t.Error("a sonda não reabriu as consultas")
	}
}
