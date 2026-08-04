package gnjoy_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// TestNoRetryDesisteNoPrimeiro429 cobre a opção usada pelas chamadas de
// fundo (checagem da watchlist, aquecimento do action id): quando o site está
// pedindo calma, elas desistem de imediato em vez de insistir — o ciclo
// seguinte refaz a consulta de qualquer forma, e insistir só disputaria a
// cota com as ações que o usuário está esperando.
func TestNoRetryDesisteNoPrimeiro429(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)

	_, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada",
	}, gnjoy.NoRetry())
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}

	var httpErr *gnjoy.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("erro = %v, quero um *gnjoy.HTTPError com status 429", err)
	}
	if got := srv.RequestCount(); got != 1 {
		t.Errorf("houve %d requisições, quero 1 (nenhuma nova tentativa)", got)
	}
}

// TestCalmariaGlobalApos429 é a garantia central do cooldown compartilhado:
// um 429 recebido por UMA chamada segura TODAS as seguintes até o fim do
// Retry-After, em vez de cada uma descobrir o bloqueio tomando o próprio 429.
func TestCalmariaGlobalApos429(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "1"}, 1)

	params := gnjoy.SearchShopsParams{ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada"}

	// A primeira chamada toma o 429 e falha rápido (NoRetry), mas deixa a
	// calmaria de 1s registrada no client.
	if _, err := client.SearchShops(context.Background(), params, gnjoy.NoRetry()); err == nil {
		t.Fatal("esperava erro na primeira chamada, veio nil")
	}

	// A segunda chamada não tem falha nenhuma na fila do mock — mas ainda
	// assim só pode disparar quando a calmaria acabar.
	began := time.Now()
	if _, err := client.SearchShops(context.Background(), params); err != nil {
		t.Fatalf("segunda chamada: %v", err)
	}
	elapsed := time.Since(began)

	// Folga de 30% contra flakiness; o que reprova é disparar de imediato.
	if elapsed < 700*time.Millisecond {
		t.Errorf("a segunda chamada disparou após %v, quero ≥ ~1s (a calmaria do 429 não foi respeitada)", elapsed)
	}
	if got := srv.RequestCount(); got != 2 {
		t.Errorf("houve %d requisições, quero 2 (o 429 e a que passou depois da calmaria)", got)
	}
}

// TestCalmariaRespeitaCancelamento: uma chamada aguardando a calmaria não
// pode ficar presa quando quem pediu desiste (ex.: o usuário fechou a aba).
func TestCalmariaRespeitaCancelamento(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "60"}, 1)

	params := gnjoy.SearchShopsParams{ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada"}
	if _, err := client.SearchShops(context.Background(), params, gnjoy.NoRetry()); err == nil {
		t.Fatal("esperava erro na primeira chamada, veio nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.SearchShops(ctx, params)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("erro = %v, quero context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a chamada cancelada não retornou: ficou presa aguardando a calmaria")
	}
	if got := srv.RequestCount(); got != 1 {
		t.Errorf("houve %d requisições, quero 1 (a cancelada não pode ter saído)", got)
	}
}

// TestDescobertaAbortaAVarreduraNoPrimeiro429: no meio da varredura de chunks
// da redescoberta do action id, um 429 significa que o site está pedindo
// calma AGORA — e ainda haveria vários chunks pela frente. A varredura
// inteira precisa parar ali, em vez de seguir chunk a chunk contra um site
// que está recusando.
func TestDescobertaAbortaAVarreduraNoPrimeiro429(t *testing.T) {
	var chunkHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_next/static/chunks/") {
			chunkHits.Add(1)
			w.Header().Set("Retry-After", "0")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("content-type", "text/html")
		fmt.Fprint(w, `<script src="/_next/static/chunks/a-1111111111111111.js"></script>`+
			`<script src="/_next/static/chunks/b-2222222222222222.js"></script>`)
	}))
	defer srv.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(srv.URL), gnjoy.WithRateLimit(1000, 1000))

	_, err := client.RefreshActionID(context.Background())
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("erro = %v, quero menção ao 429 que interrompeu a varredura", err)
	}
	if got := chunkHits.Load(); got != 1 {
		t.Errorf("a varredura consultou %d chunks, quero que pare no primeiro 429", got)
	}
}
