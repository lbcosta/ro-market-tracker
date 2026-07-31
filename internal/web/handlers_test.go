package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

func TestMain(m *testing.M) {
	// Os handlers logam os erros de upstream que os testes provocam de
	// propósito; sem isso a saída do teste fica poluída de ruído esperado.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// newWebServer sobe o frontend real ligado ao site falso do GnJoy — nenhum
// teste toca a API de verdade.
func newWebServer(t *testing.T) (*httptest.Server, *gnjoytest.Server) {
	t.Helper()
	return newWebServerWith(t, gnjoytest.DemoConfig())
}

func newWebServerWith(t *testing.T, cfg gnjoytest.Config) (*httptest.Server, *gnjoytest.Server) {
	t.Helper()

	mock := gnjoytest.New(cfg)
	t.Cleanup(mock.Close)

	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	)

	mux := http.NewServeMux()
	RegisterRoutes(mux, client)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, mock
}

func getHTML(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lendo corpo de %s: %v", path, err)
	}
	return resp, string(body)
}

// wantContains falha o teste listando o HTML inteiro quando um trecho
// esperado não aparece — sem isso, depurar uma falha de template é adivinhação.
func wantContains(t *testing.T, html string, trechos ...string) {
	t.Helper()
	for _, trecho := range trechos {
		if !strings.Contains(html, trecho) {
			t.Errorf("HTML não contém %q.\nHTML:\n%s", trecho, html)
		}
	}
}

func TestIndex(t *testing.T) {
	srv, mock := newWebServer(t)

	resp, html := getHTML(t, srv, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}
	if got := resp.Header.Get("content-type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("content-type = %q, quero text/html", got)
	}

	wantContains(t, html,
		"RO Market Tracker",
		`hx-get="/web/search"`,
		`id="watchlist-list"`,
		`id="activity-bar"`,
		`id="activity-history"`,
		`id="watchlist-countdown"`,
	)

	// A página em si não consulta o mercado: só a busca faz isso.
	if got := mock.RequestCount(); got != 0 {
		t.Errorf("carregar a página custou %d requisições ao upstream, quero 0", got)
	}
}

func TestArquivosEstaticos(t *testing.T) {
	srv, _ := newWebServer(t)

	// Os assets são embutidos no binário (go:embed), então precisam ser
	// servidos sem depender de nada no disco nem de CDN.
	for _, path := range []string{
		"/static/style.css",
		"/static/app.js",
		"/static/watchlist.js",
		"/static/activity-bar.js",
		"/static/htmx.min.js",
	} {
		resp, body := getHTML(t, srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, quero 200", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Errorf("GET %s: corpo vazio", path)
		}
	}
}

func TestSearch(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada"}}
	resp, html := getHTML(t, srv, "/web/search?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}

	wantContains(t, html,
		// O título usa o nome canônico do primeiro resultado, não o que o
		// usuário digitou.
		"Resultados de Espada Primordial",
		"Espada Citadina",
		"Carta Peixe-Espada",
		"129.999.999 z",
		"Vendinha do Zé",
		// O botão da watchlist precisa carregar o itemId, já que buscar pelo
		// nome pode casar itens diferentes.
		`data-item-id="600009"`,
		`data-server="NIDHOGG"`,
		// Cada linha é expansível e só busca o detalhe uma vez.
		`hx-get="/web/shops/303/835/s-primordial-129/expand"`,
		`hx-trigger="click once"`,
	)

	// A busca da página é sempre por lojas comprando o item (anúncios de
	// jogadores vendendo) — não há seletor de tipo na UI.
	if got := mock.Requests()[0].Query.Get("storeType"); got != "BUY" {
		t.Errorf("storeType = %q, quero \"BUY\"", got)
	}
}

func TestSearchSemResultados(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"item que ninguém anuncia"}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	wantContains(t, html, "Nenhum resultado encontrado.")
	if strings.Contains(html, "results-table") {
		t.Error("uma busca sem resultados não deveria renderizar a tabela")
	}
}

func TestSearchParametrosFaltando(t *testing.T) {
	srv, mock := newWebServer(t)

	tests := []struct {
		name  string
		query url.Values
	}{
		{"sem item", url.Values{"server": {"NIDHOGG"}}},
		{"sem server", url.Values{"item": {"Espada"}}},
		{"item só com espaços", url.Values{"server": {"NIDHOGG"}, "item": {"   "}}},
		{"nada", url.Values{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ResetRequests()

			_, html := getHTML(t, srv, "/web/search?"+tt.query.Encode())
			wantContains(t, html, `class="error"`, "Informe o servidor e o nome do item.")

			if got := mock.RequestCount(); got != 0 {
				t.Errorf("houve %d requisições ao upstream, quero 0", got)
			}
		})
	}
}

func TestSearchErroDoUpstream(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada"}}
	resp, html := getHTML(t, srv, "/web/search?"+q.Encode())

	// O fragmento é trocado no DOM pelo HTMX, então precisa vir com 200 e a
	// mensagem de erro dentro — um status de erro faria o HTMX descartá-lo.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, quero 200 (o HTMX precisa trocar o fragmento)", resp.StatusCode)
	}
	wantContains(t, html, `class="error"`, "Não foi possível buscar no mercado agora.")
}

// TestSearchEscapaHTML garante que um nome de loja com marcação (os jogadores
// escolhem esses nomes livremente) seja exibido como texto, não interpretado.
func TestSearchEscapaHTML(t *testing.T) {
	mock := gnjoytest.New(gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			"Espada": {Items: []gnjoytest.ShopListItem{{
				SvrId: 303, MapId: 835, SSI: "s1", ItemId: 1,
				ItemName:  "Espada",
				StoreName: `<script>alert("xss")</script>`,
				ItemPrice: 100, ItemCnt: 1,
			}}},
		},
	})
	defer mock.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(mock.URL), gnjoy.WithRateLimit(1000, 1000))
	mux := http.NewServeMux()
	RegisterRoutes(mux, client)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, html := getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")

	if strings.Contains(html, "<script>alert") {
		t.Errorf("o nome da loja foi injetado sem escape:\n%s", html)
	}
	wantContains(t, html, "&lt;script&gt;")
}

func TestExpand(t *testing.T) {
	srv, _ := newWebServer(t)

	resp, html := getHTML(t, srv, "/web/shops/303/835/s-primordial-158/expand")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}

	wantContains(t, html,
		// O "+" do prefixo de refino sai escapado como &#43; pelo
		// html/template, então a asserção é sobre o resto do nome.
		"Espada Primordial[2]",
		// O refino ganha um badge próprio além de aparecer no nome.
		`class="refine-badge">+7`,
		"158.000.000 z",
		"Wololol",
		"mercatung",
		// O comando /navi vem sem a extensão .gat do nome do mapa.
		"/navi prt_mk/120/150",
		// Estatísticas dos dias com histórico (ver fixtures do mock).
		"Últimos 3 dias",
		"500 z",      // mínimo
		"1.750 z",    // média ponderada
		"5.000 z",    // máximo
		"829 z",      // desvio padrão
		"<dd>4</dd>", // quantidade vendida
	)
}

// TestExpandSemRefino confirma que o badge só aparece quando há refino: um
// item não refinável não deve ganhar um "+0" enganoso.
func TestExpandSemRefino(t *testing.T) {
	srv, _ := newWebServer(t)

	_, html := getHTML(t, srv, "/web/shops/303/835/s-carta-peixe/expand")

	wantContains(t, html, "Carta Peixe-Espada")
	if strings.Contains(html, "refine-badge") {
		t.Errorf("um item sem refino não deveria ter badge de refino:\n%s", html)
	}
}

func TestExpandSemHistorico(t *testing.T) {
	srv, _ := newWebServer(t)

	_, html := getHTML(t, srv, "/web/shops/303/835/p-carta-noel/expand")

	wantContains(t, html, "Carta Poring Noel", "Sem histórico de vendas recente.")
}

func TestExpandPathInvalido(t *testing.T) {
	srv, mock := newWebServer(t)

	tests := []struct {
		name string
		path string
	}{
		{"svrId não numérico", "/web/shops/abc/835/s-citadina/expand"},
		{"mapId não numérico", "/web/shops/303/xyz/s-citadina/expand"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ResetRequests()

			resp, _ := getHTML(t, srv, tt.path)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, quero 400", resp.StatusCode)
			}
			if got := mock.RequestCount(); got != 0 {
				t.Errorf("houve %d requisições ao upstream, quero 0", got)
			}
		})
	}
}

// TestExpandFalhaEmCadaEtapa cobre as três chamadas que o card de detalhe
// precisa fazer: cada uma tem sua própria mensagem, para o usuário saber o que
// falhou em vez de ver um card vazio.
func TestExpandFalhaEmCadaEtapa(t *testing.T) {
	tests := []struct {
		name         string
		passthroughs int
		wantMensagem string
		wantAusencia string
	}{
		{
			name:         "detalhe da loja",
			passthroughs: 0,
			wantMensagem: "Não foi possível carregar os detalhes dessa loja agora.",
			wantAusencia: "detail-card",
		},
		{
			name:         "detalhe do item",
			passthroughs: 1,
			wantMensagem: "Não foi possível carregar os detalhes desse item agora.",
			wantAusencia: "detail-card",
		},
		{
			name:         "histórico de preço",
			passthroughs: 2,
			wantMensagem: "Não foi possível carregar o histórico de preços agora.",
			wantAusencia: "detail-card",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, mock := newWebServer(t)

			// Deixa passar as chamadas anteriores e derruba a que interessa
			// (e as repetições que o client tentar depois dela).
			mock.QueueFailure(gnjoytest.Failure{Passthrough: true}, tt.passthroughs)
			mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 20)

			resp, html := getHTML(t, srv, "/web/shops/303/835/s-primordial-158/expand")
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, quero 200 (o HTMX precisa trocar o fragmento)", resp.StatusCode)
			}
			wantContains(t, html, `class="error"`, tt.wantMensagem)
			if strings.Contains(html, tt.wantAusencia) {
				t.Errorf("o card não deveria ser renderizado quando a etapa falha:\n%s", html)
			}
		})
	}
}
