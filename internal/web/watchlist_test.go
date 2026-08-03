package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

type watchlistPriceResponse struct {
	Found    bool  `json:"found"`
	MinPrice int64 `json:"minPrice"`
	Refine   *int  `json:"refine"`
}

// TestWatchlistPriceMenorPreco é o caso padrão da watchlist: sem refino
// fixado, o painel mostra o anúncio mais barato daquele itemId — e, se for
// equipamento, o refino daquela unidade em específico (só informativo).
func TestWatchlistPriceMenorPreco(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
	resp, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found {
		t.Fatal("Found = false, quero true")
	}
	if view.MinPrice != 129999999 {
		t.Errorf("MinPrice = %d, quero 129999999 (o mais barato dos três anúncios)", view.MinPrice)
	}
	if view.Refine == nil {
		t.Fatal("Refine = nil, quero o refino da unidade mais barata")
	}
	if *view.Refine != 0 {
		t.Errorf("Refine = %d, quero 0 (o anúncio mais barato é o sem refino)", *view.Refine)
	}

	// Uma busca + o detalhe da loja mais barata: nada além disso.
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("houve %d requisições ao upstream, quero 2", got)
	}
}

// TestWatchlistPriceFiltraPorItemId cobre por que a watchlist guarda o itemId
// e não só o nome: buscar "Espada" traz itens diferentes que compartilham a
// palavra, e o preço alvo é de um item específico.
func TestWatchlistPriceFiltraPorItemId(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"1147"}, "item": {"Espada"}}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	// 59.000 é a Espada Citadina; o menor preço geral da busca por "Espada"
	// seria esse mesmo, mas o que importa é ter vindo do itemId pedido.
	if !view.Found || view.MinPrice != 59000 {
		t.Errorf("resultado = %+v, quero o preço da Espada Citadina (59000)", view)
	}
}

// TestWatchlistPriceItemNaoEquipamento confirma que itens sem refino não
// ganham um "+0" enganoso: o campo simplesmente não vem.
func TestWatchlistPriceItemNaoEquipamento(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"4089"}, "item": {"Espada"}}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 4999999 {
		t.Errorf("resultado = %+v, quero found com preço 4999999", view)
	}
	if view.Refine != nil {
		t.Errorf("Refine = %d, quero ausente para um item que não é equipamento", *view.Refine)
	}

	// Sem equipamento, não há motivo para consultar o detalhe da loja.
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("houve %d requisições ao upstream, quero só a busca", got)
	}
}

// TestWatchlistPriceComRefinoFixado é o caminho caro: o refino só é conhecido
// consultando o detalhe de cada loja, então achar "o mais barato NESSE refino"
// custa uma chamada por candidato até encontrar.
func TestWatchlistPriceComRefinoFixado(t *testing.T) {
	tests := []struct {
		name         string
		refine       string
		wantFound    bool
		wantPrice    int64
		wantRequests int
	}{
		{
			// Primeiro candidato (o mais barato) já bate.
			name: "refino do anúncio mais barato", refine: "0",
			wantFound: true, wantPrice: 129999999, wantRequests: 2,
		},
		{
			// Busca + detalhe do 1º (não bate) + detalhe do 2º (bate).
			name: "refino intermediário", refine: "7",
			wantFound: true, wantPrice: 158000000, wantRequests: 3,
		},
		{
			// Precisa varrer os três candidatos até achar.
			name: "refino do anúncio mais caro", refine: "10",
			wantFound: true, wantPrice: 299999999, wantRequests: 4,
		},
		{
			// Nenhum anúncio nesse refino: varre todos e não acha.
			name: "refino que ninguém anuncia", refine: "3",
			wantFound: false, wantRequests: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, mock := newWebServer(t)

			q := url.Values{
				"server": {"NIDHOGG"}, "itemId": {"600009"},
				"item": {"Espada"}, "refine": {tt.refine},
			}
			_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

			var view watchlistPriceResponse
			if err := json.Unmarshal([]byte(body), &view); err != nil {
				t.Fatalf("corpo inválido: %s", body)
			}
			if view.Found != tt.wantFound {
				t.Fatalf("Found = %v, quero %v (corpo: %s)", view.Found, tt.wantFound, body)
			}
			if tt.wantFound {
				if view.MinPrice != tt.wantPrice {
					t.Errorf("MinPrice = %d, quero %d", view.MinPrice, tt.wantPrice)
				}
				wantRefine, err := strconv.Atoi(tt.refine)
				if err != nil {
					t.Fatalf("refino do caso de teste inválido: %v", err)
				}
				if view.Refine == nil || *view.Refine != wantRefine {
					t.Errorf("Refine = %v, quero %d", view.Refine, wantRefine)
				}
			}
			if got := mock.RequestCount(); got != tt.wantRequests {
				t.Errorf("houve %d requisições ao upstream, quero %d", got, tt.wantRequests)
			}
		})
	}
}

func TestWatchlistPriceItemSemAnuncios(t *testing.T) {
	srv, _ := newWebServer(t)

	// itemId que não aparece em nenhum resultado da busca.
	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"999999"}, "item": {"Espada"}}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if view.Found {
		t.Errorf("Found = true, quero false para um item sem anúncios")
	}
}

func TestWatchlistPriceParametrosInvalidos(t *testing.T) {
	srv, mock := newWebServer(t)

	tests := []struct {
		name  string
		query url.Values
	}{
		{"sem server", url.Values{"itemId": {"600009"}, "item": {"Espada"}}},
		{"sem item", url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}}},
		{"sem itemId", url.Values{"server": {"NIDHOGG"}, "item": {"Espada"}}},
		{"itemId não numérico", url.Values{"server": {"NIDHOGG"}, "itemId": {"abc"}, "item": {"Espada"}}},
		{"refino não numérico", url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}, "refine": {"muito"}}},
		{"refino negativo", url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}, "refine": {"-1"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ResetRequests()

			resp, body := getHTML(t, srv, "/web/watchlist/price?"+tt.query.Encode())
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, quero 400; corpo: %s", resp.StatusCode, body)
			}
			if got := mock.RequestCount(); got != 0 {
				t.Errorf("houve %d requisições ao upstream, quero 0", got)
			}
		})
	}
}

// TestWatchlistPriceRefinoVazio garante que o parâmetro opcional ausente não
// é confundido com "refino 0": o frontend omite o parâmetro ao desfixar.
func TestWatchlistPriceRefinoVazio(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}, "refine": {""}}
	resp, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}
	// Duas requisições = caminho "menor preço", não o caminho de filtro.
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("houve %d requisições, quero 2 (o refino vazio não deve virar filtro)", got)
	}
}

func TestWatchlistPriceErroNaBusca(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
	resp, _ := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, quero 502", resp.StatusCode)
	}
}

// configComLojaSumida devolve as fixtures de sempre, mas sem o detalhe da
// loja mais barata — o que reproduz uma corrida real deste domínio: o anúncio
// aparece na busca e, quando o detalhe é consultado, o jogador já fechou a
// barraca. O site responde a action com success:false.
func configComLojaSumida() gnjoytest.Config {
	cfg := gnjoytest.DemoConfig()
	delete(cfg.Stores, "s-primordial-129")
	return cfg
}

// TestWatchlistPriceRefinoIndisponivel: se a busca funcionou mas o detalhe da
// loja não, ainda dá para mostrar o preço — só o refino é que fica de fora.
// Perder o preço por causa de um dado acessório seria pior.
func TestWatchlistPriceRefinoIndisponivel(t *testing.T) {
	srv, _ := newWebServerWith(t, configComLojaSumida())

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
	resp, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 129999999 {
		t.Errorf("resultado = %+v, quero o preço mesmo sem conseguir o refino", view)
	}
	if view.Refine != nil {
		t.Errorf("Refine = %d, quero ausente quando o detalhe da loja não veio", *view.Refine)
	}
}

// TestWatchlistPriceFiltroComCandidatoIndisponivel: se o detalhe de um
// candidato não vem, a varredura segue para o próximo em vez de desistir.
func TestWatchlistPriceFiltroComCandidatoIndisponivel(t *testing.T) {
	srv, _ := newWebServerWith(t, configComLojaSumida())

	q := url.Values{
		"server": {"NIDHOGG"}, "itemId": {"600009"},
		"item": {"Espada"}, "refine": {"7"},
	}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 158000000 {
		t.Errorf("resultado = %+v, quero o anúncio +7 (158000000) apesar de o primeiro candidato ter sumido", view)
	}
}

// TestWatchlistPriceItemComHifenNoNome é a regressão de um item que ficava
// impossível de acompanhar: a watchlist consulta o mercado pelo nome canônico
// do item, e o backend do GnJoy responde a página de erro dele para qualquer
// termo com hífen (ver gnjoy.splitSearchWord). O painel mostrava
// "Indisponível" para sempre — o item nunca seria encontrado, mesmo estando
// à venda.
func TestWatchlistPriceItemComHifenNoNome(t *testing.T) {
	srv, mock := newWebServerWith(t, gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			// Semeado sob o termo que o client tem de enviar no lugar do nome
			// completo. O mock recusa hífen como o site real, então um
			// retrocesso no contorno derruba este teste.
			"Módulo de S": {Items: []gnjoytest.ShopListItem{
				{SvrId: 303, MapId: 835, SSI: "mod-rapidez", ItemId: 25690, ItemName: "Módulo de S-Rapidez", ItemPrice: 6000000, ItemCnt: 1},
				{SvrId: 303, MapId: 835, SSI: "mod-forca", ItemId: 25691, ItemName: "Módulo de S-Força", ItemPrice: 500000, ItemCnt: 1},
			}},
		},
	})

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"25690"}, "item": {"Módulo de S-Rapidez"}}
	resp, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found {
		t.Fatal("Found = false, quero true — o item está anunciado")
	}
	if view.MinPrice != 6000000 {
		t.Errorf("MinPrice = %d, quero 6000000", view.MinPrice)
	}

	if got := mock.Requests()[0].Query.Get("searchWord"); got != "Módulo de S" {
		t.Errorf("searchWord enviado ao upstream = %q, quero \"Módulo de S\" (sem hífen)", got)
	}
}

// TestWatchlistPriceItemComMaisNoNome é a mesma regressão que
// TestWatchlistPriceItemComHifenNoNome, para o outro caractere que o backend
// do GnJoy recusa: "+". As caixas de refino têm o nível embutido no próprio
// nome do item ("Caixa de Arma +13"), e era justamente esse nome canônico
// que a watchlist mandava de volta ao consultar o preço ao vivo — o painel
// mostrava "Indisponível" para um item que estava, de fato, à venda.
func TestWatchlistPriceItemComMaisNoNome(t *testing.T) {
	srv, mock := newWebServerWith(t, gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			// Semeado sob o termo que o client tem de enviar no lugar do nome
			// completo. O mock recusa "+" como o site real, então um
			// retrocesso no contorno derruba este teste.
			"Caixa de Arma": {Items: []gnjoytest.ShopListItem{
				{SvrId: 303, MapId: 835, SSI: "caixa-7", ItemId: 22911, ItemName: "Caixa de Arma +7", ItemPrice: 1000000, ItemCnt: 1},
				{SvrId: 303, MapId: 835, SSI: "caixa-13", ItemId: 22917, ItemName: "Caixa de Arma +13", ItemPrice: 9000000, ItemCnt: 1},
			}},
		},
	})

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"22917"}, "item": {"Caixa de Arma +13"}}
	resp, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found {
		t.Fatal("Found = false, quero true — o item está anunciado")
	}
	if view.MinPrice != 9000000 {
		t.Errorf("MinPrice = %d, quero 9000000", view.MinPrice)
	}

	if got := mock.Requests()[0].Query.Get("searchWord"); got != "Caixa de Arma" {
		t.Errorf("searchWord enviado ao upstream = %q, quero \"Caixa de Arma\" (sem \"+\")", got)
	}
}
