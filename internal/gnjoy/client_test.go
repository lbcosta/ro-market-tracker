package gnjoy_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// newTestClient sobe o site falso e devolve um client apontado para ele.
//
// O rate limit é deliberadamente alto: o padrão do client (1 req/s) existe
// para não irritar o site real, e manter isso nos testes só os deixaria
// lentos. O comportamento do rate limiter em si é testado à parte, em
// TestRateLimiterEnfileiraRequisicoesConcorrentes.
func newTestClient(t *testing.T, cfg gnjoytest.Config, opts ...gnjoy.Option) (*gnjoy.Client, *gnjoytest.Server) {
	t.Helper()
	srv := gnjoytest.New(cfg)
	t.Cleanup(srv.Close)

	base := []gnjoy.Option{
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID(srv.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	}
	return gnjoy.New(append(base, opts...)...), srv
}

func TestSearchShops(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG",
		StoreType:  gnjoy.StoreTypeBuy,
		SearchWord: "Espada",
	})
	if err != nil {
		t.Fatalf("SearchShops: %v", err)
	}

	if got, want := result.TotalCount, 5; got != want {
		t.Errorf("TotalCount = %d, quero %d", got, want)
	}
	if got, want := len(result.Items), 5; got != want {
		t.Fatalf("len(Items) = %d, quero %d", got, want)
	}

	first := result.Items[0]
	if first.ItemId != 600009 || first.ItemName != "Espada Primordial" {
		t.Errorf("primeiro item = (%d, %q), quero (600009, \"Espada Primordial\")", first.ItemId, first.ItemName)
	}
	if first.ItemPrice != 129999999 {
		t.Errorf("ItemPrice = %d, quero 129999999", first.ItemPrice)
	}
	if first.SSI != "s-primordial-129" {
		t.Errorf("SSI = %q, quero \"s-primordial-129\"", first.SSI)
	}
	if first.StoreName != "Vendinha do Zé" {
		t.Errorf("StoreName = %q, quero \"Vendinha do Zé\"", first.StoreName)
	}
	if first.DatabaseType != "weapon" {
		t.Errorf("DatabaseType = %q, quero \"weapon\"", first.DatabaseType)
	}

	// A busca precisa chegar ao site com os filtros na query string e com os
	// cabeçalhos que fazem o Next.js responder Flight em vez do HTML.
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("esperava 1 requisição ao upstream, houve %d", len(reqs))
	}
	req := reqs[0]
	if got := req.Query.Get("searchWord"); got != "Espada" {
		t.Errorf("searchWord = %q, quero \"Espada\"", got)
	}
	if got := req.Query.Get("serverType"); got != "NIDHOGG" {
		t.Errorf("serverType = %q, quero \"NIDHOGG\"", got)
	}
	if got := req.Query.Get("storeType"); got != "BUY" {
		t.Errorf("storeType = %q, quero \"BUY\"", got)
	}
	if got := req.Header.Get("rsc"); got != "1" {
		t.Errorf("cabeçalho rsc = %q, quero \"1\"", got)
	}
	if got := req.Header.Get("user-agent"); !strings.Contains(got, "Mozilla/5.0") {
		t.Errorf("user-agent = %q, quero algo parecido com um navegador", got)
	}
	if got := req.Header.Get("next-router-state-tree"); got == "" {
		t.Error("next-router-state-tree não foi enviado")
	}
}

func TestSearchShopsSemResultados(t *testing.T) {
	client, _ := newTestClient(t, gnjoytest.DemoConfig())

	result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG",
		StoreType:  gnjoy.StoreTypeBuy,
		SearchWord: "item que ninguém anuncia",
	})
	if err != nil {
		t.Fatalf("SearchShops: %v", err)
	}
	if len(result.Items) != 0 || result.TotalCount != 0 {
		t.Errorf("resultado = %+v, quero vazio", result)
	}
}

// TestBuscaComHifen cobre o contorno do defeito do upstream: o backend do
// GnJoy responde a página de erro dele para qualquer termo com hífen, então o
// client manda o maior pedaço sem hífen e filtra a resposta pelo termo
// inteiro. Sem isso, itens com hífen no nome ("Módulo de S-Rapidez", "Carta
// Peixe-Espada") são impossíveis de buscar — e o mock reproduz a falha, então
// mandar o termo cru quebraria estes testes.
func TestBuscaComHifen(t *testing.T) {
	cfg := gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			// O termo que o client precisa enviar no lugar de "Módulo de
			// S-Rapidez": o maior pedaço sem hífen. Casa por trecho do nome,
			// então traz também itens que o termo inteiro não traria.
			"Módulo de S": {Items: []gnjoytest.ShopListItem{
				{SvrId: 303, MapId: 835, SSI: "m1", ItemId: 25690, ItemName: "Módulo de S-Rapidez", ItemPrice: 6000000, ItemCnt: 1},
				{SvrId: 303, MapId: 835, SSI: "m2", ItemId: 25691, ItemName: "Módulo de S-Força", ItemPrice: 3000000, ItemCnt: 1},
			}},
		},
		MarketPrices: map[gnjoytest.MarketPriceScope]gnjoytest.MarketPriceResult{
			{ServerType: "NIDHOGG", SearchWord: "Módulo de S", Period: "7"}: {Items: []gnjoytest.MarketPriceItem{
				{SvrId: 303, ItemId: 25690, ItemName: "Módulo de S-Rapidez", TotalItemCnt: 2, MinItemPrice: 5000000, AvgItemPrice: 7500000, MaxItemPrice: 10000000},
				{SvrId: 303, ItemId: 25691, ItemName: "Módulo de S-Força", TotalItemCnt: 9, MinItemPrice: 1000000, AvgItemPrice: 2000000, MaxItemPrice: 3000000},
			}},
		},
	}

	t.Run("busca de lojas", func(t *testing.T) {
		client, srv := newTestClient(t, cfg)

		result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
			ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Módulo de S-Rapidez",
		})
		if err != nil {
			t.Fatalf("SearchShops: %v", err)
		}

		if got := srv.Requests()[0].Query.Get("searchWord"); got != "Módulo de S" {
			t.Errorf("searchWord enviado = %q, quero \"Módulo de S\" (o maior pedaço sem hífen)", got)
		}
		if len(result.Items) != 1 || result.Items[0].ItemName != "Módulo de S-Rapidez" {
			t.Fatalf("itens = %+v, quero só o \"Módulo de S-Rapidez\"", result.Items)
		}
		// O total precisa acompanhar a lista filtrada; o do upstream é o da
		// busca ampliada e mentiria sobre o que foi devolvido.
		if result.TotalCount != 1 {
			t.Errorf("TotalCount = %d, quero 1", result.TotalCount)
		}
	})

	t.Run("preços de mercado", func(t *testing.T) {
		client, srv := newTestClient(t, cfg)

		result, err := client.SearchMarketPrice(context.Background(), gnjoy.MarketPriceParams{
			ServerType: "NIDHOGG", SearchWord: "Módulo de S-Rapidez", Period: gnjoy.MarketPricePeriodWeek,
		})
		if err != nil {
			t.Fatalf("SearchMarketPrice: %v", err)
		}

		if got := srv.Requests()[0].Query.Get("searchWord"); got != "Módulo de S" {
			t.Errorf("searchWord enviado = %q, quero \"Módulo de S\"", got)
		}
		if len(result.Items) != 1 || result.Items[0].ItemName != "Módulo de S-Rapidez" {
			t.Fatalf("itens = %+v, quero só o \"Módulo de S-Rapidez\"", result.Items)
		}
		if result.TotalCount != 1 {
			t.Errorf("TotalCount = %d, quero 1", result.TotalCount)
		}
	})

	// O pedaço escolhido é o maior, e não o primeiro: quanto mais específico
	// o termo enviado, menos itens irrelevantes voltam para filtrar.
	t.Run("escolhe o maior pedaço", func(t *testing.T) {
		client, srv := newTestClient(t, gnjoytest.Config{})

		if _, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
			ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "S-Rapidez",
		}); err != nil {
			t.Fatalf("SearchShops: %v", err)
		}
		if got := srv.Requests()[0].Query.Get("searchWord"); got != "Rapidez" {
			t.Errorf("searchWord enviado = %q, quero \"Rapidez\"", got)
		}
	})

	// Um termo só de hífens não tem pedaço nenhum a procurar, e enviar vazio
	// devolveria o mercado inteiro — melhor nem consultar o upstream.
	t.Run("termo só de hífens não consulta o upstream", func(t *testing.T) {
		client, srv := newTestClient(t, gnjoytest.DemoConfig())

		result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
			ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "--",
		})
		if err != nil {
			t.Fatalf("SearchShops: %v", err)
		}
		if len(result.Items) != 0 {
			t.Errorf("itens = %+v, quero vazio", result.Items)
		}
		if got := srv.RequestCount(); got != 0 {
			t.Errorf("requisições ao upstream = %d, quero 0", got)
		}
	})
}

// TestBuscaComMais cobre o mesmo defeito do upstream que TestBuscaComHifen,
// só que para "+": itens de caixa de refino têm o nível embutido no próprio
// nome do item de catálogo ("Caixa de Arma +13", "Caixa de Armadura +7"), e é
// justamente esse nome canônico que a watchlist manda de volta ao consultar
// o preço ao vivo (ver internal/web/watchlist.go) — sem o contorno, esses
// itens ficam impossíveis de acompanhar.
func TestBuscaComMais(t *testing.T) {
	cfg := gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			// O termo que o client precisa enviar no lugar de "Caixa de Arma
			// +13": o maior pedaço sem "+". Casa por trecho do nome, então
			// traz também as outras caixas de refino.
			"Caixa de Arma": {Items: []gnjoytest.ShopListItem{
				{SvrId: 303, MapId: 835, SSI: "c7", ItemId: 22911, ItemName: "Caixa de Arma +7", ItemPrice: 1000000, ItemCnt: 1},
				{SvrId: 303, MapId: 835, SSI: "c13", ItemId: 22917, ItemName: "Caixa de Arma +13", ItemPrice: 9000000, ItemCnt: 1},
			}},
		},
	}

	client, srv := newTestClient(t, cfg)

	result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Caixa de Arma +13",
	})
	if err != nil {
		t.Fatalf("SearchShops: %v", err)
	}

	if got := srv.Requests()[0].Query.Get("searchWord"); got != "Caixa de Arma" {
		t.Errorf("searchWord enviado = %q, quero \"Caixa de Arma\" (o maior pedaço sem \"+\")", got)
	}
	if len(result.Items) != 1 || result.Items[0].ItemName != "Caixa de Arma +13" {
		t.Fatalf("itens = %+v, quero só a \"Caixa de Arma +13\"", result.Items)
	}
	if result.TotalCount != 1 {
		t.Errorf("TotalCount = %d, quero 1", result.TotalCount)
	}
}

// TestBuscaComPontuacao é a generalização de TestBuscaComHifen e
// TestBuscaComMais: o backend do GnJoy não recusa dois caracteres em
// particular, ele aceita SÓ letras, dígitos e espaços. Cada caso abaixo é um
// item real que ficava impossível de buscar — e de acompanhar na watchlist,
// que consulta o mercado pelo nome canônico do item.
func TestBuscaComPontuacao(t *testing.T) {
	tests := []struct {
		name     string
		termo    string
		wantSend string
	}{
		{
			// O caso relatado: as pedras de encantamento trazem a posição do
			// encantamento entre parênteses no nome do item.
			name: "parênteses", termo: "Pedra de Mestre II (Baixo)", wantSend: "Pedra de Mestre II",
		},
		{
			// Os visuais têm um prefixo entre colchetes.
			name: "colchetes", termo: "[Visual] Chapéu Confeitado", wantSend: "Chapéu Confeitado",
		},
		{
			name: "ponto", termo: "Sr. Cavaleiro", wantSend: "Cavaleiro",
		},
		{
			name: "apóstrofo", termo: "Asa d'Anjo Gigante", wantSend: "Anjo Gigante",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := gnjoytest.Config{
				Searches: map[string]gnjoytest.SearchResult{
					// Semeado sob o trecho que o client tem de enviar: o mock
					// recusa a pontuação como o site real faz.
					tt.wantSend: {Items: []gnjoytest.ShopListItem{
						{SvrId: 303, MapId: 835, SSI: "achado", ItemId: 42, ItemName: tt.termo, ItemPrice: 100, ItemCnt: 1},
						{SvrId: 303, MapId: 835, SSI: "outro", ItemId: 43, ItemName: tt.wantSend + " Comum", ItemPrice: 50, ItemCnt: 1},
					}},
				},
			}
			client, srv := newTestClient(t, cfg)

			result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
				ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: tt.termo,
			})
			if err != nil {
				t.Fatalf("SearchShops: %v", err)
			}
			if got := srv.Requests()[0].Query.Get("searchWord"); got != tt.wantSend {
				t.Errorf("searchWord enviado = %q, quero %q (o maior trecho aceito pelo upstream)", got, tt.wantSend)
			}
			if len(result.Items) != 1 || result.Items[0].ItemName != tt.termo {
				t.Fatalf("itens = %+v, quero só %q", result.Items, tt.termo)
			}
		})
	}
}

// TestBuscaPeloNomeComSlots cobre o termo como o usuário o vê na tela: o site
// devolve o nome do item e o sufixo de slots em campos separados, então uma
// busca por "Selo de Loki [1]" — o nome exibido — precisa achar o anúncio
// cujo ItemName é só "Selo de Loki" e cujo SlotMaxCount é "[1]".
func TestBuscaPeloNomeComSlots(t *testing.T) {
	cfg := gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			"Selo de Loki": {Items: []gnjoytest.ShopListItem{
				{SvrId: 303, MapId: 835, SSI: "sem-slot", ItemId: 410232, ItemName: "Selo de Loki", ItemPrice: 79999999, ItemCnt: 1},
				{SvrId: 303, MapId: 835, SSI: "com-slot", ItemId: 410233, ItemName: "Selo de Loki", SlotMaxCount: "[1]", ItemPrice: 250000000, ItemCnt: 1},
			}},
		},
	}
	client, _ := newTestClient(t, cfg)

	result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Selo de Loki [1]",
	})
	if err != nil {
		t.Fatalf("SearchShops: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ItemId != 410233 {
		t.Fatalf("itens = %+v, quero só a versão com slot (410233)", result.Items)
	}
	if got := result.Items[0].DisplayName(); got != "Selo de Loki [1]" {
		t.Errorf("DisplayName = %q, quero \"Selo de Loki [1]\"", got)
	}
}

func TestSearchMarketPrice(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	result, err := client.SearchMarketPrice(context.Background(), gnjoy.MarketPriceParams{
		ServerType: "NIDHOGG",
		SearchWord: "Rapidez",
		Period:     gnjoy.MarketPricePeriodWeek,
	})
	if err != nil {
		t.Fatalf("SearchMarketPrice: %v", err)
	}

	// O termo casa por trecho do nome, então a lista tem mais de um item — é
	// o que distingue esta busca da consulta de histórico por itemId.
	if got, want := len(result.Items), 2; got != want {
		t.Fatalf("len(Items) = %d, quero %d", got, want)
	}
	if got, want := result.TotalCount, 2; got != want {
		t.Errorf("TotalCount = %d, quero %d", got, want)
	}

	first := result.Items[0]
	if first.ItemId != 1000125 || first.ItemName != "Automódulo de M-Rapidez" {
		t.Errorf("primeiro item = (%d, %q), quero (1000125, \"Automódulo de M-Rapidez\")", first.ItemId, first.ItemName)
	}
	if first.MinItemPrice != 5000000 || first.AvgItemPrice != 8666666 || first.MaxItemPrice != 12000000 {
		t.Errorf("preços = (%d, %d, %d), quero (5000000, 8666666, 12000000)",
			first.MinItemPrice, first.AvgItemPrice, first.MaxItemPrice)
	}
	if first.TotalItemCnt != 3 {
		t.Errorf("TotalItemCnt = %d, quero 3", first.TotalItemCnt)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("esperava 1 requisição ao upstream, houve %d", len(reqs))
	}
	req := reqs[0]
	if !strings.HasSuffix(req.Path, "/intro/shop-search/market-price") {
		t.Errorf("path = %q, quero a página de preços de mercado", req.Path)
	}
	for param, want := range map[string]string{
		"serverType": "NIDHOGG",
		"searchWord": "Rapidez",
		"period":     "7",
	} {
		if got := req.Query.Get(param); got != want {
			t.Errorf("%s = %q, quero %q", param, got, want)
		}
	}
	if got := req.Header.Get("rsc"); got != "1" {
		t.Errorf("cabeçalho rsc = %q, quero \"1\"", got)
	}
	// A árvore de rotas precisa apontar para esta página, e não para a de
	// comércio: é ela que diz ao Next.js que navegação está sendo feita.
	if got := req.Header.Get("next-router-state-tree"); !strings.Contains(got, url.QueryEscape("market-price")) {
		t.Errorf("next-router-state-tree = %q, quero a rota de market-price", got)
	}
}

// TestSearchMarketPriceSemPeriodo confirma que omitir o período não manda o
// parâmetro vazio: o site trata a ausência dele como "todo o histórico".
func TestSearchMarketPriceSemPeriodo(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	if _, err := client.SearchMarketPrice(context.Background(), gnjoy.MarketPriceParams{
		ServerType: "NIDHOGG", SearchWord: "Rapidez",
	}); err != nil {
		t.Fatalf("SearchMarketPrice: %v", err)
	}

	if _, ok := srv.Requests()[0].Query["period"]; ok {
		t.Error("o parâmetro period foi enviado mesmo sem ter sido informado")
	}
}

// TestSearchMarketPriceSemResultados: um item nunca vendido devolve lista
// vazia, não erro — é assim que o site responde.
func TestSearchMarketPriceSemResultados(t *testing.T) {
	client, _ := newTestClient(t, gnjoytest.DemoConfig())

	result, err := client.SearchMarketPrice(context.Background(), gnjoy.MarketPriceParams{
		ServerType: "NIDHOGG", SearchWord: "Elmo Ancestral", Period: gnjoy.MarketPricePeriodAll,
	})
	if err != nil {
		t.Fatalf("SearchMarketPrice: %v", err)
	}
	if len(result.Items) != 0 || result.TotalCount != 0 {
		t.Errorf("resultado = %+v, quero vazio", result)
	}
}

func TestSearchMarketPriceErroDoUpstream(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError, Body: "boom"}, 1)

	_, err := client.SearchMarketPrice(context.Background(), gnjoy.MarketPriceParams{
		ServerType: "NIDHOGG", SearchWord: "Rapidez", Period: gnjoy.MarketPricePeriodWeek,
	})
	var httpErr *gnjoy.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("erro = %v, quero um *gnjoy.HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, quero 500", httpErr.StatusCode)
	}
}

func TestSearchShopsErroDoUpstream(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError, Body: "boom"}, 1)

	_, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada",
	})
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}

	var httpErr *gnjoy.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("erro = %v, quero um *gnjoy.HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, quero 500", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Body, "boom") {
		t.Errorf("Body = %q, quero que contenha \"boom\"", httpErr.Body)
	}
}

func TestSearchShopsRespostaSemOsCamposEsperados(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Malformed: true}, 1)

	_, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada",
	})
	if !errors.Is(err, gnjoy.ErrFieldsNotFound) {
		t.Fatalf("erro = %v, quero ErrFieldsNotFound", err)
	}
}

// TestGetStoreDetailRefino cobre o principal detalhe do formato do site: o
// refino não é um campo próprio, vem embutido como prefixo "+N" do nome
// completo do item — e é 0 tanto para "sem refino" quanto para "não
// refinável".
func TestGetStoreDetailRefino(t *testing.T) {
	client, _ := newTestClient(t, gnjoytest.DemoConfig())

	tests := []struct {
		ssi        string
		wantRefine int
		wantName   string
	}{
		{"s-primordial-129", 0, "Espada Primordial[2]"},
		{"s-primordial-158", 7, "+7Espada Primordial[2]"},
		{"s-primordial-299", 10, "+10Espada Primordial[2]"},
		{"s-carta-peixe", 0, "Carta Peixe-Espada"},
	}
	for _, tt := range tests {
		t.Run(tt.ssi, func(t *testing.T) {
			detail, err := client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{
				SvrId: 303, MapId: 835, SSI: tt.ssi,
			})
			if err != nil {
				t.Fatalf("GetStoreDetail: %v", err)
			}
			if detail.Refine != tt.wantRefine {
				t.Errorf("Refine = %d, quero %d", detail.Refine, tt.wantRefine)
			}
			if detail.ItemFullName != tt.wantName {
				t.Errorf("ItemFullName = %q, quero %q", detail.ItemFullName, tt.wantName)
			}
			if detail.SSI != tt.ssi {
				t.Errorf("SSI = %q, quero %q", detail.SSI, tt.ssi)
			}
			if detail.MapName != "prt_mk.gat" {
				t.Errorf("MapName = %q, quero \"prt_mk.gat\"", detail.MapName)
			}
		})
	}
}

func TestGetStoreDetailActionSemSucesso(t *testing.T) {
	client, _ := newTestClient(t, gnjoytest.DemoConfig())

	// ssi desconhecido faz o mock responder success:false, como o site faz
	// quando a loja não existe mais (o jogador fechou a barraca).
	_, err := client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "loja-que-nao-existe",
	})
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}
	if !strings.Contains(err.Error(), "reportou falha") {
		t.Errorf("erro = %v, quero uma menção a falha reportada pela action", err)
	}
}

func TestGetItemDetail(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	detail, err := client.GetItemDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "s-citadina",
	}, "")
	if err != nil {
		t.Fatalf("GetItemDetail: %v", err)
	}
	if detail.ItemName != "Espada Citadina" || detail.ItemId != 1147 {
		t.Errorf("item = (%d, %q), quero (1147, \"Espada Citadina\")", detail.ItemId, detail.ItemName)
	}
	if detail.ItemType != "weapon" {
		t.Errorf("ItemType = %q, quero \"weapon\"", detail.ItemType)
	}
	if detail.RandomOpt1 != nil {
		t.Errorf("RandomOpt1 = %v, quero nil", *detail.RandomOpt1)
	}

	// lang vazio precisa virar o padrão "en-US" no payload enviado.
	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if !strings.Contains(last.Body, `"multiLan":"en-US"`) {
		t.Errorf("payload = %s, quero multiLan \"en-US\"", last.Body)
	}
}

func TestGetItemDetailLangExplicito(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	if _, err := client.GetItemDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "s-citadina",
	}, "pt-BR"); err != nil {
		t.Fatalf("GetItemDetail: %v", err)
	}

	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if !strings.Contains(last.Body, `"multiLan":"pt-BR"`) {
		t.Errorf("payload = %s, quero multiLan \"pt-BR\"", last.Body)
	}
}

func TestGetPriceHistory(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	history, err := client.GetPriceHistory(context.Background(), gnjoy.PriceHistoryParams{
		ItemId: 600009, SvrId: 303,
	})
	if err != nil {
		t.Fatalf("GetPriceHistory: %v", err)
	}
	if len(history.DayStatsList) != 3 {
		t.Fatalf("len(DayStatsList) = %d, quero 3", len(history.DayStatsList))
	}
	if history.DayStatsList[0].AvgItemPrice != 2000 {
		t.Errorf("AvgItemPrice do primeiro dia = %d, quero 2000", history.DayStatsList[0].AvgItemPrice)
	}
	if history.ItemPriceMin != 500 || history.ItemPriceMax != 5000 {
		t.Errorf("faixa = (%d, %d), quero (500, 5000)", history.ItemPriceMin, history.ItemPriceMax)
	}

	// Page e Limit zerados precisam virar os padrões 1 e 10; Period nil vira
	// o "$undefined" que o Next.js usa para representar um valor ausente.
	body := srv.Requests()[0].Body
	for _, want := range []string{`"page":1`, `"limit":10`, `"period":"$undefined"`} {
		if !strings.Contains(body, want) {
			t.Errorf("payload = %s, quero que contenha %s", body, want)
		}
	}
}

func TestGetPriceHistoryComParametros(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())

	period := "MONTH"
	if _, err := client.GetPriceHistory(context.Background(), gnjoy.PriceHistoryParams{
		ItemId: 600009, SvrId: 303, Page: 3, Limit: 7, Period: &period,
	}); err != nil {
		t.Fatalf("GetPriceHistory: %v", err)
	}

	body := srv.Requests()[0].Body
	for _, want := range []string{`"page":3`, `"limit":7`, `"period":"MONTH"`} {
		if !strings.Contains(body, want) {
			t.Errorf("payload = %s, quero que contenha %s", body, want)
		}
	}
}

func TestGetPriceHistoryItemSemHistorico(t *testing.T) {
	client, _ := newTestClient(t, gnjoytest.DemoConfig())

	history, err := client.GetPriceHistory(context.Background(), gnjoy.PriceHistoryParams{
		ItemId: 4005, SvrId: 303,
	})
	if err != nil {
		t.Fatalf("GetPriceHistory: %v", err)
	}
	if len(history.DayStatsList) != 0 {
		t.Errorf("DayStatsList = %+v, quero vazio", history.DayStatsList)
	}
}

// TestActionIDDesatualizadoDisparaRedescoberta cobre o cenário que acontece
// toda vez que o site é redeployado: o hash da Server Action muda, as chamadas
// passam a responder 404, e o client tem de achar o hash novo varrendo os
// chunks JS da página — sem intervenção manual e sem reiniciar o processo.
func TestActionIDDesatualizadoDisparaRedescoberta(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	defer srv.Close()

	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID("00000000000000000000000000000000deadbeef"), // hash de um deploy antigo
		gnjoy.WithRateLimit(1000, 1000),
	)

	detail, err := client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "s-primordial-158",
	})
	if err != nil {
		t.Fatalf("GetStoreDetail: %v", err)
	}
	if detail.Refine != 7 {
		t.Errorf("Refine = %d, quero 7", detail.Refine)
	}

	// Uma vez redescoberto, o hash novo fica no client: a chamada seguinte não
	// paga o custo de varrer os chunks de novo.
	srv.ResetRequests()
	if _, err := client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "s-citadina",
	}); err != nil {
		t.Fatalf("segunda GetStoreDetail: %v", err)
	}
	if got := srv.RequestCount(); got != 1 {
		t.Errorf("segunda chamada custou %d requisições, quero 1 (sem redescoberta)", got)
	}
}

func TestActionIDDesatualizadoSemChunkValido(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	defer srv.Close()

	// Todas as requisições falham: a chamada original falha e a redescoberta
	// também, e o client precisa relatar as duas coisas.
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 20)

	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID(srv.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	)

	_, err := client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "s-citadina",
	})
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}
	if !strings.Contains(err.Error(), "redescoberta do action id também falhou") {
		t.Errorf("erro = %v, quero menção à falha da redescoberta", err)
	}
}

// TestRetryApos429 confirma o comportamento defensivo do client: mesmo com o
// rate limiter local, se o site responder 429 o client espera o tempo pedido
// e tenta de novo, em vez de propagar o erro na primeira tentativa.
func TestRetryApos429(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{
		Status:     http.StatusTooManyRequests,
		RetryAfter: "0",
	}, 2)

	result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Poring",
	})
	if err != nil {
		t.Fatalf("SearchShops: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("len(Items) = %d, quero 1", len(result.Items))
	}
	if got := srv.RequestCount(); got != 3 {
		t.Errorf("houve %d requisições, quero 3 (duas recusadas + a que passou)", got)
	}
}

func TestRetryApos429SeEsgota(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	// Mais 429 do que o client está disposto a tolerar.
	srv.QueueFailure(gnjoytest.Failure{
		Status:     http.StatusTooManyRequests,
		RetryAfter: "0",
	}, 20)

	_, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Poring",
	})
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}

	// O erro precisa continuar sendo reconhecível como 429 para que a API
	// consiga traduzi-lo em 503 (ver internal/api.writeUpstreamError).
	var httpErr *gnjoy.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("erro = %v, quero um *gnjoy.HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, quero 429", httpErr.StatusCode)
	}
	// 1 tentativa original + 5 retries.
	if got := srv.RequestCount(); got != 6 {
		t.Errorf("houve %d requisições, quero 6", got)
	}
}

// TestRetryApos429RefazOCorpoDoPost garante que uma Server Action (POST) possa
// ser repetida: o corpo já foi consumido na tentativa anterior e precisa ser
// recriado, senão a repetição iria com o corpo vazio.
func TestRetryApos429RefazOCorpoDoPost(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"}, 1)

	detail, err := client.GetStoreDetail(context.Background(), gnjoy.StoreLocation{
		SvrId: 303, MapId: 835, SSI: "s-primordial-158",
	})
	if err != nil {
		t.Fatalf("GetStoreDetail: %v", err)
	}
	if detail.Refine != 7 {
		t.Errorf("Refine = %d, quero 7", detail.Refine)
	}

	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("houve %d requisições, quero 2", len(reqs))
	}
	if reqs[1].Body == "" {
		t.Error("a repetição foi enviada com o corpo vazio")
	}
	if reqs[0].Body != reqs[1].Body {
		t.Errorf("corpo da repetição = %q, quero igual ao da primeira tentativa %q", reqs[1].Body, reqs[0].Body)
	}
}

// TestRateLimiterEnfileiraRequisicoesConcorrentes é a garantia central de que
// o client nunca vai estourar o rate limiter do site: mesmo com várias
// goroutines disparando ao mesmo tempo, as requisições saem espaçadas pelo
// ritmo configurado, em fila, em vez de todas de uma vez.
func TestRateLimiterEnfileiraRequisicoesConcorrentes(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	defer srv.Close()

	const rps = 20.0
	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID(srv.ActionID()),
		gnjoy.WithRateLimit(rps, 1),
	)

	const n = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
				ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Poring",
			})
		}()
	}
	began := time.Now()
	close(start) // dispara todas as goroutines ao mesmo tempo
	wg.Wait()
	elapsed := time.Since(began)

	if got := srv.RequestCount(); got != n {
		t.Fatalf("houve %d requisições, quero %d", got, n)
	}

	// Com burst 1, a primeira sai na hora e as outras n-1 ficam em fila,
	// espaçadas de 1/rps. Uma folga generosa evita flakiness em máquina
	// carregada, mas ainda reprova o caso em que o limiter não atrasa nada.
	minElapsed := time.Duration(float64(n-1)/rps*float64(time.Second)) * 8 / 10
	if elapsed < minElapsed {
		t.Errorf("as %d requisições levaram %v, quero pelo menos %v (o limiter não enfileirou)", n, elapsed, minElapsed)
	}
}

// TestRateLimiterRespeitaCancelamento garante que uma requisição em fila não
// fica presa quando o cliente HTTP original desiste (ex.: o usuário fechou a
// aba e o contexto da requisição HTTP foi cancelado).
func TestRateLimiterRespeitaCancelamento(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.DemoConfig())
	defer srv.Close()

	// Ritmo lento o bastante para que a segunda chamada fique esperando muito
	// mais do que a duração do teste.
	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithActionID(srv.ActionID()),
		gnjoy.WithRateLimit(0.01, 1),
	)

	params := gnjoy.SearchShopsParams{ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Poring"}
	if _, err := client.SearchShops(context.Background(), params); err != nil {
		t.Fatalf("primeira busca: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.SearchShops(ctx, params)
		done <- err
	}()

	// Dá tempo de a segunda chamada entrar na fila antes de cancelar.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("erro = %v, quero context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a busca cancelada não retornou: ficou presa na fila do rate limiter")
	}

	if got := srv.RequestCount(); got != 1 {
		t.Errorf("houve %d requisições, quero 1 (a cancelada não pode ter saído)", got)
	}
}

func TestOpcoesDeConstrucao(t *testing.T) {
	srv := gnjoytest.New(gnjoytest.Config{
		Locale:   "es",
		ActionID: gnjoytest.DefaultActionID,
		Searches: map[string]gnjoytest.SearchResult{
			"Poring": {Items: []gnjoytest.ShopListItem{{ItemId: 4005, ItemName: "Carta Poring"}}},
		},
	})
	defer srv.Close()

	client := gnjoy.New(
		gnjoy.WithBaseURL(srv.URL),
		gnjoy.WithLocale("es"),
		gnjoy.WithActionID(srv.ActionID()),
		gnjoy.WithUserAgent("ro-market-tracker-teste/1.0"),
		gnjoy.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
		gnjoy.WithRateLimit(1000, 1000),
	)

	result, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "FREYA", StoreType: gnjoy.StoreTypeSell, SearchWord: "Poring",
	})
	if err != nil {
		t.Fatalf("SearchShops: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len(Items) = %d, quero 1", len(result.Items))
	}

	req := srv.Requests()[0]
	if req.Path != "/es/intro/shop-search/trading" {
		t.Errorf("caminho = %q, quero o locale \"es\" na rota", req.Path)
	}
	if got := req.Header.Get("user-agent"); got != "ro-market-tracker-teste/1.0" {
		t.Errorf("user-agent = %q, quero o customizado", got)
	}
	if got := req.Query.Get("storeType"); got != "SELL" {
		t.Errorf("storeType = %q, quero \"SELL\"", got)
	}
}

func TestHTTPErrorMensagem(t *testing.T) {
	err := &gnjoy.HTTPError{StatusCode: 503, Body: "indisponível"}
	msg := err.Error()
	if !strings.Contains(msg, "503") || !strings.Contains(msg, "indisponível") {
		t.Errorf("Error() = %q, quero que inclua status e corpo", msg)
	}
}

// TestCorpoDeErroTruncado evita que uma página de erro gigante do upstream
// vire uma mensagem de erro (e uma linha de log) de megabytes.
func TestCorpoDeErroTruncado(t *testing.T) {
	client, srv := newTestClient(t, gnjoytest.DemoConfig())
	srv.QueueFailure(gnjoytest.Failure{
		Status: http.StatusBadGateway,
		Body:   strings.Repeat("x", 10_000),
	}, 1)

	_, err := client.SearchShops(context.Background(), gnjoy.SearchShopsParams{
		ServerType: "NIDHOGG", StoreType: gnjoy.StoreTypeBuy, SearchWord: "Espada",
	})
	var httpErr *gnjoy.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("erro = %v, quero um *gnjoy.HTTPError", err)
	}
	if len(httpErr.Body) > 1000 {
		t.Errorf("len(Body) = %d, quero um corpo truncado", len(httpErr.Body))
	}
}
