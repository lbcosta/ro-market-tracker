package gnjoy_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

func TestRefreshActionID(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	defer srv.Close()

	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID("hash-de-um-deploy-antigo"),
		gnjoy.WithRateLimit(1000, 1000),
	)

	id, err := client.RefreshActionID(context.Background())
	if err != nil {
		t.Fatalf("RefreshActionID: %v", err)
	}
	if id != srv.ActionID() {
		t.Errorf("action id = %q, quero %q", id, srv.ActionID())
	}
}

// TestRefreshActionIDAposDeployDoSite simula o que de fato acontece em
// produção: o site é redeployado e o hash muda enquanto o processo está no ar.
func TestRefreshActionIDAposDeployDoSite(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	defer srv.Close()

	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID(srv.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	)

	loc := gnjoy.StoreLocation{SvrId: 303, MapId: 835, SSI: "s-citadina"}
	if _, err := client.GetStoreDetail(context.Background(), loc); err != nil {
		t.Fatalf("chamada antes do deploy: %v", err)
	}

	const novoHash = "fedcba9876543210fedcba9876543210fedcba98"
	srv.SetActionID(novoHash)

	// Sem nenhuma intervenção, a próxima chamada precisa se recuperar sozinha.
	detail, err := client.GetStoreDetail(context.Background(), loc)
	if err != nil {
		t.Fatalf("chamada depois do deploy: %v", err)
	}
	if detail.SSI != "s-citadina" {
		t.Errorf("SSI = %q, quero \"s-citadina\"", detail.SSI)
	}

	id, err := client.RefreshActionID(context.Background())
	if err != nil {
		t.Fatalf("RefreshActionID: %v", err)
	}
	if id != novoHash {
		t.Errorf("action id = %q, quero %q", id, novoHash)
	}
}

func TestRefreshActionIDPaginaSemChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		fmt.Fprint(w, "<!doctype html><html><body>página sem nenhum script</body></html>")
	}))
	defer srv.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(srv.URL), gnjoy.WithRateLimit(1000, 1000))

	_, err := client.RefreshActionID(context.Background())
	if !errors.Is(err, gnjoy.ErrActionIDNotFound) {
		t.Fatalf("erro = %v, quero ErrActionIDNotFound", err)
	}
	if !strings.Contains(err.Error(), "nenhum chunk JS") {
		t.Errorf("erro = %v, quero que explique que não há chunks na página", err)
	}
}

func TestRefreshActionIDNenhumChunkDeclaraAAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_next/static/chunks/") {
			fmt.Fprint(w, "console.log('nada de createServerReference por aqui');")
			return
		}
		w.Header().Set("content-type", "text/html")
		fmt.Fprint(w, `<script src="/_next/static/chunks/a-1111111111111111.js"></script>`+
			`<script src="/_next/static/chunks/b-2222222222222222.js"></script>`)
	}))
	defer srv.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(srv.URL), gnjoy.WithRateLimit(1000, 1000))

	_, err := client.RefreshActionID(context.Background())
	if !errors.Is(err, gnjoy.ErrActionIDNotFound) {
		t.Fatalf("erro = %v, quero ErrActionIDNotFound", err)
	}
}

func TestRefreshActionIDPaginaIndisponivel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fora do ar", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(srv.URL), gnjoy.WithRateLimit(1000, 1000))

	_, err := client.RefreshActionID(context.Background())
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}
	if !strings.Contains(err.Error(), "buscando página para descoberta") {
		t.Errorf("erro = %v, quero menção à busca da página", err)
	}
}

// TestUpstreamInalcancavel cobre o caminho de erro de rede (não é um status
// HTTP): a API precisa conseguir traduzir isso em 502 mesmo assim.
func TestUpstreamInalcancavel(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	baseURL := srv.URL
	srv.Close() // ninguém mais escutando nessa porta

	client := gnjoy.New(gnjoy.WithBaseURL(baseURL), gnjoy.WithRateLimit(1000, 1000))

	_, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada",
	})
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}
	var httpErr *gnjoy.HTTPError
	if errors.As(err, &httpErr) {
		t.Errorf("erro = %v, um erro de rede não deveria virar HTTPError", err)
	}
}
