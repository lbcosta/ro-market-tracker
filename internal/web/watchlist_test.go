package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

type watchlistPriceResponse struct {
	Found       bool   `json:"found"`
	MinPrice    int64  `json:"minPrice"`
	Refine      *int   `json:"refine"`
	NaviCommand string `json:"naviCommand"`
	Partial     bool   `json:"partial"`
	Equipment   *bool  `json:"equipment"`
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
	if view.NaviCommand != "/navi prt_mk.gat 114/180" {
		t.Errorf("NaviCommand = %q, quero a localização da loja mais barata", view.NaviCommand)
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
// ganham um "+0" enganoso: o campo simplesmente não vem. A localização, essa
// sim, vem — ela não depende do item ser equipamento (ver comentário de
// watchlistPriceForCheapest).
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
	if view.NaviCommand != "/navi prt_mk.gat 100/100" {
		t.Errorf("NaviCommand = %q, quero a localização da loja mesmo sem equipamento", view.NaviCommand)
	}

	// Busca + o detalhe da loja mais barata (agora sempre consultado, para a
	// localização — ver watchlistPriceForCheapest).
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("houve %d requisições ao upstream, quero 2", got)
	}
}

// TestWatchlistPriceComRefinoMinimo é o caminho caro: o refino só é conhecido
// consultando o detalhe de cada loja, então achar "o mais barato a partir
// desse refino" custa uma chamada por candidato até encontrar.
//
// O filtro é um PISO, não um valor exato: refino maior só melhora a unidade,
// então quem acompanha +7 quer saber de um +9 barato também.
func TestWatchlistPriceComRefinoMinimo(t *testing.T) {
	tests := []struct {
		name         string
		refine       string
		wantFound    bool
		wantPrice    int64
		wantRefine   int
		wantNavi     string
		wantRequests int
	}{
		{
			// Primeiro candidato (o mais barato) já bate.
			name: "refino do anúncio mais barato", refine: "0",
			wantFound: true, wantPrice: 129999999, wantRefine: 0,
			wantNavi: "/navi prt_mk.gat 114/180", wantRequests: 2,
		},
		{
			// Busca + detalhe do 1º (não bate) + detalhe do 2º (bate).
			name: "refino intermediário", refine: "7",
			wantFound: true, wantPrice: 158000000, wantRefine: 7,
			wantNavi: "/navi prt_mk.gat 120/150", wantRequests: 3,
		},
		{
			// Precisa varrer os três candidatos até achar.
			name: "refino do anúncio mais caro", refine: "10",
			wantFound: true, wantPrice: 299999999, wantRefine: 10,
			wantNavi: "/navi prt_mk.gat 99/42", wantRequests: 4,
		},
		{
			// O contrato do piso: ninguém anuncia +3, mas o +7 serve — e é
			// o mais barato entre os que servem. Com igualdade exata este
			// caso respondia "sem anúncios" com o mercado cheio deles.
			name: "refino que ninguém anuncia aceita o acima dele", refine: "3",
			wantFound: true, wantPrice: 158000000, wantRefine: 7,
			wantNavi: "/navi prt_mk.gat 120/150", wantRequests: 3,
		},
		{
			// Acima de tudo que existe: varre todos e não acha.
			name: "refino acima de todos os anúncios", refine: "11",
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
				// O refino DEVOLVIDO é o do anúncio encontrado, que pode
				// ser maior que o pedido — é o que a linha mostra ao lado
				// do preço para o usuário saber o que vai comprar.
				if view.Refine == nil || *view.Refine != tt.wantRefine {
					t.Errorf("Refine = %v, quero %d", view.Refine, tt.wantRefine)
				}
				if view.NaviCommand != tt.wantNavi {
					t.Errorf("NaviCommand = %q, quero %q", view.NaviCommand, tt.wantNavi)
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
		{"bônus demais", url.Values{
			"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"},
			// Cinco, um a mais do que os quatro que o site expõe por anúncio.
			"bonus": {"CRIT +1", "CRIT +2", "CRIT +3", "CRIT +4", "CRIT +5"},
		}},
		{"bônus gigante", url.Values{
			"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"},
			"bonus": {strings.Repeat("x", maxBonusFilterLen+1)},
		}},
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
	if view.NaviCommand != "" {
		t.Errorf("NaviCommand = %q, quero ausente quando o detalhe da loja não veio", view.NaviCommand)
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

// --- filtro por bônus aleatórios ---
//
// As fixtures servem bem a estes testes: dos três anúncios de Espada
// Primordial, o de 129 milhões não tem bônus, o de 158 tem "CRIT +4" E
// "ATQ +3%", e o de 299 tem só "CRIT +4". Ou seja, há um bônus compartilhado
// por dois anúncios, e um deles com um bônus a mais — que é o caso que
// distingue "tem o que pedi" de "tem exatamente o que pedi".

// TestWatchlistPriceComBonusFixado: o filtro não devolve o anúncio mais
// barato, devolve o mais barato QUE SERVE.
func TestWatchlistPriceComBonusFixado(t *testing.T) {
	tests := []struct {
		name      string
		bonus     []string
		wantFound bool
		wantPrice int64
	}{
		{
			// 129 não tem bônus nenhum, então o mais barato que serve é o 158
			// — que tem "CRIT +4" e ainda um bônus a mais, o que não o
			// desqualifica.
			name: "um bônus, presente em dois anúncios", bonus: []string{"CRIT +4"},
			wantFound: true, wantPrice: 158000000,
		},
		{
			// Só o 158 tem este.
			name: "bônus exclusivo de um anúncio", bonus: []string{"ATQ +3%"},
			wantFound: true, wantPrice: 158000000,
		},
		{
			// E lógico: só o 158 tem os dois.
			name: "dois bônus exigidos ao mesmo tempo", bonus: []string{"CRIT +4", "ATQ +3%"},
			wantFound: true, wantPrice: 158000000,
		},
		{
			// A comparação normaliza caixa e espaço sobrando — o campo é
			// digitado à mão.
			name: "grafia com caixa e espaço diferentes", bonus: []string{"  crit   +4 "},
			wantFound: true, wantPrice: 158000000,
		},
		{
			name: "bônus que ninguém anuncia", bonus: []string{"SP máx. +846"},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newWebServer(t)

			q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
			q["bonus"] = tt.bonus
			_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

			var view watchlistPriceResponse
			if err := json.Unmarshal([]byte(body), &view); err != nil {
				t.Fatalf("corpo inválido: %s", body)
			}
			if view.Found != tt.wantFound {
				t.Fatalf("Found = %v, quero %v (corpo: %s)", view.Found, tt.wantFound, body)
			}
			if tt.wantFound && view.MinPrice != tt.wantPrice {
				t.Errorf("MinPrice = %d, quero %d", view.MinPrice, tt.wantPrice)
			}
			// Poucos anúncios: a varredura alcança todos, então "não achei" é
			// definitivo — nada de Partial.
			if view.Partial {
				t.Error("Partial = true, quero false: a varredura cobriu todos os candidatos")
			}
		})
	}
}

// TestWatchlistPriceBonusTrazLocalizacao: com filtro só de bônus, o detalhe da
// LOJA (de onde sai o /navi e o refino) não é consultado durante a varredura —
// e ainda assim precisa ser buscado para o anúncio vencedor.
func TestWatchlistPriceBonusTrazLocalizacao(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
	q["bonus"] = []string{"ATQ +3%"}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if view.NaviCommand != "/navi prt_mk.gat 120/150" {
		t.Errorf("NaviCommand = %q, quero a localização do anúncio que casou", view.NaviCommand)
	}
	if view.Refine == nil || *view.Refine != 7 {
		t.Errorf("Refine = %v, quero 7 (o anúncio que casou é o +7)", view.Refine)
	}
}

// TestWatchlistPriceRefinoRejeitaAntesDoBonus é a garantia de que a ordem da
// varredura economiza requisição: um candidato reprovado pelo refino não chega
// a ter o detalhe do item consultado.
func TestWatchlistPriceRefinoRejeitaAntesDoBonus(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{
		"server": {"NIDHOGG"}, "itemId": {"600009"},
		"item": {"Espada"}, "refine": {"10"},
	}
	q["bonus"] = []string{"CRIT +4"}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 299999999 {
		t.Fatalf("resultado = %+v, quero o anúncio +10 com CRIT +4 (299999999)", view)
	}

	// Busca + 3 detalhes de loja (os dois primeiros reprovam pelo refino, o
	// terceiro passa) + 1 detalhe de item (só do que passou no refino). Se o
	// bônus fosse consultado antes do refino, seriam 3 detalhes de item em vez
	// de 1.
	if got := mock.RequestCount(); got != 5 {
		t.Errorf("houve %d requisições ao upstream, quero 5 (busca + 3 lojas + 1 item)", got)
	}
}

// TestWatchlistPriceBonusEmNaoEquipamento: carta nunca tem bônus aleatório, e
// o site nem oferece a rota — descartar pelo tipo, que já vem na busca, evita
// gastar requisição para descobrir o óbvio.
func TestWatchlistPriceBonusEmNaoEquipamento(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"4089"}, "item": {"Espada"}}
	q["bonus"] = []string{"CRIT +4"}
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if view.Found {
		t.Errorf("Found = true, quero false: uma carta não tem bônus aleatório")
	}
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("houve %d requisições ao upstream, quero só a busca", got)
	}
}

// TestWatchlistPriceBonusVazioNaoViraFiltro: o front-end desfixa um campo
// mandando ele vazio, do mesmo jeito que faz com o refino.
func TestWatchlistPriceBonusVazioNaoViraFiltro(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
	q["bonus"] = []string{"", "   "}
	resp, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 129999999 {
		t.Errorf("resultado = %+v, quero o mais barato (o campo vazio não filtra nada)", view)
	}
	// Duas requisições = caminho "mais barato", não o da varredura com filtro.
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("houve %d requisições, quero 2 (o bônus vazio não deve virar filtro)", got)
	}
}

// TestWatchlistPriceInformaSeEEquipamento: é o "equipment" que decide se a
// linha da watchlist oferece os campos de bônus. Ele não depende de o filtro
// ter achado alguma coisa — só de haver anúncio de onde ler o tipo.
func TestWatchlistPriceInformaSeEEquipamento(t *testing.T) {
	tests := []struct {
		name   string
		itemId string
		bonus  []string
		want   *bool
	}{
		{name: "equipamento", itemId: "600009", want: ptr(true)},
		{name: "carta", itemId: "4089", want: ptr(false)},
		{name: "item sem anúncio nenhum", itemId: "999999", want: nil},
		{
			// O ponto do campo: mesmo sem achar anúncio que sirva, a linha
			// precisa continuar oferecendo os campos de bônus — é neles que o
			// usuário conserta o que digitou.
			name: "equipamento com filtro que não casa", itemId: "600009",
			bonus: []string{"SP máx. +846"}, want: ptr(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newWebServer(t)

			q := url.Values{"server": {"NIDHOGG"}, "itemId": {tt.itemId}, "item": {"Espada"}}
			q["bonus"] = tt.bonus
			_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

			var view watchlistPriceResponse
			if err := json.Unmarshal([]byte(body), &view); err != nil {
				t.Fatalf("corpo inválido: %s", body)
			}
			switch {
			case tt.want == nil && view.Equipment != nil:
				t.Errorf("Equipment = %v, quero ausente (não há anúncio de onde saber o tipo)", *view.Equipment)
			case tt.want != nil && view.Equipment == nil:
				t.Errorf("Equipment ausente, quero %v", *tt.want)
			case tt.want != nil && *view.Equipment != *tt.want:
				t.Errorf("Equipment = %v, quero %v", *view.Equipment, *tt.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// TestWatchlistPriceBonusCoberturaCresceEntreCiclos é o contrato central do
// custo: uma checagem gasta no máximo maxDetailFetches consultas novas, então
// um item com muitos anúncios não é resolvido de primeira — e enquanto não
// for, a resposta diz "parcial" em vez de mentir que não existe.
func TestWatchlistPriceBonusCoberturaCresceEntreCiclos(t *testing.T) {
	if maxDetailFetches != 8 {
		t.Fatalf("maxDetailFetches = %d; este teste assume 8 — reveja as contas dele", maxDetailFetches)
	}

	// Doze anúncios do mesmo item, e só o mais caro com o bônus procurado:
	// mais candidatos do que o orçamento de uma checagem alcança.
	items := make([]gnjoytest.ShopListItem, 0, 12)
	itemDetails := make(map[string]gnjoytest.ItemDetail, 12)
	stores := make(map[string]gnjoytest.StoreDetail, 12)
	for i := 1; i <= 12; i++ {
		ssi := fmt.Sprintf("adaga-%d", i)
		items = append(items, gnjoytest.ShopListItem{
			SvrId: 303, MapId: 835, SSI: ssi, ItemId: 777,
			ItemName: "Adaga Rúnica", DatabaseType: "weapon",
			ItemPrice: int64(i) * 1000, ItemCnt: 1,
		})
		stores[ssi] = gnjoytest.StoreDetail{
			SvrId: 303, MapId: 835, SSI: ssi, ItemId: 777,
			ItemFullName: "Adaga Rúnica", ItemPrice: int64(i) * 1000, ItemCnt: 1,
			MapName: "prt_mk.gat", Xpos: "100", Ypos: "100", DatabaseType: "weapon",
		}
		detail := gnjoytest.ItemDetail{
			SvrId: 303, MapId: 835, SSI: ssi, ItemId: 777,
			ItemName: "Adaga Rúnica", ItemPrice: int64(i) * 1000, DatabaseType: "weapon",
		}
		if i == 12 {
			procurado := "CRIT +5"
			detail.RandomOpt1 = &procurado
		}
		itemDetails[ssi] = detail
	}

	srv, mock := newWebServerWith(t, gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{"Adaga": {Items: items}},
		Stores:   stores,
		Items:    itemDetails,
	})

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"777"}, "item": {"Adaga"}}
	q["bonus"] = []string{"CRIT +5"}
	path := "/web/watchlist/price?" + q.Encode()

	_, body := getHTML(t, srv, path)
	var primeira watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &primeira); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if primeira.Found {
		t.Fatalf("primeira checagem achou (%+v); quero que o orçamento a interrompa antes do 12º anúncio", primeira)
	}
	if !primeira.Partial {
		t.Error("Partial = false na primeira checagem, quero true: a varredura não chegou ao fim")
	}
	if got := mock.RequestCount(); got != 1+maxDetailFetches {
		t.Errorf("primeira checagem custou %d requisições, quero %d (busca + orçamento)", got, 1+maxDetailFetches)
	}

	// Segunda checagem: a busca vem do cache e os 8 primeiros detalhes já
	// estão memoizados, então o orçamento inteiro avança para anúncios novos —
	// e alcança o 12º.
	_, body = getHTML(t, srv, path)
	var segunda watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &segunda); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !segunda.Found || segunda.MinPrice != 12000 {
		t.Errorf("segunda checagem = %+v, quero o 12º anúncio (12000)", segunda)
	}
	if segunda.Partial {
		t.Error("Partial = true na segunda checagem, quero false: a varredura alcançou o anúncio")
	}
}
