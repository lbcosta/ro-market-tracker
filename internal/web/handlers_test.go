package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

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
	srv, _ := newWebServer(t)

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

	// Abrir a página dispara, em segundo plano, o aquecimento do action id
	// (ver TestIndexAquecePreviamenteOActionID) — então, ao contrário da
	// busca em si, ela pode custar requisições ao upstream, só que
	// assíncronas e fora do caminho crítico da resposta.
}

// TestIndexAquecePreviamenteOActionID garante que abrir a página já dispara
// em segundo plano um teste do action id da Server Action do Next.js, em vez
// de deixar a primeira ação de verdade do usuário (expandir uma linha) topar
// com o id desatualizado e pagar o custo da redescoberta no meio da
// interação — o que dava a impressão de o site estar lento. Como o
// aquecimento roda em background, o teste espera (com um prazo curto) até a
// chamada chegar ao mock, em vez de checar o efeito imediatamente após a
// resposta de "/". No caso comum (id já válido, como aqui), o teste custa
// uma única requisição — a redescoberta completa não é necessária.
func TestIndexAquecePreviamenteOActionID(t *testing.T) {
	srv, mock := newWebServer(t)

	if _, err := srv.Client().Get(srv.URL + "/"); err != nil {
		t.Fatalf("GET /: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for mock.RequestCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("requisições ao upstream após abrir a página = %d, quero 1 (a chamada de teste do action id, sem redescoberta completa já que o id atual já é válido)", got)
	}
}

// TestIndexAquecimentoSoDisparaUmaVez garante que recarregar a página várias
// vezes não repete o custo do aquecimento a cada carregamento — só a
// primeira chamada a "/" do processo dispara o teste do action id.
func TestIndexAquecimentoSoDisparaUmaVez(t *testing.T) {
	srv, mock := newWebServer(t)

	for i := 0; i < 3; i++ {
		if _, err := srv.Client().Get(srv.URL + "/"); err != nil {
			t.Fatalf("GET / (%d): %v", i, err)
		}
	}

	// Dá tempo de sobra para qualquer aquecimento (ou repetição indevida
	// dele) terminar antes de conferir o total.
	time.Sleep(200 * time.Millisecond)

	// O número exato não importa aqui, só que ele pare de crescer depois da
	// primeira rodada — nenhuma das duas recargas seguintes deveria disparar
	// outro teste do action id.
	first := mock.RequestCount()
	time.Sleep(200 * time.Millisecond)
	if second := mock.RequestCount(); second != first {
		t.Errorf("requisições ao upstream continuaram crescendo (%d -> %d) depois de várias cargas da página; o aquecimento deveria disparar só uma vez", first, second)
	}
}

func TestArquivosEstaticos(t *testing.T) {
	srv, _ := newWebServer(t)

	// Os assets são embutidos no binário (go:embed), então precisam ser
	// servidos sem depender de nada no disco nem de CDN.
	for _, path := range []string{
		"/static/style.css",
		"/static/app.js",
		"/static/theme.js",
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
		// O título mostra o termo digitado — a busca por palavra pode casar
		// itens de nomes diferentes, então não há "o" nome canônico do
		// resultado para usar aqui (cada seção mostra o seu).
		"Resultados de Espada",
		"Espada Primordial",
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

// TestSearchAgrupaItensDeNomesDiferentes é a regressão do caso em que
// "Espada" casa três itens diferentes (Espada Primordial em três preços,
// Espada Citadina, Carta Peixe-Espada): um único "+ Watchlist" no topo da
// tabela não tinha como dizer qual desses itens ele adicionava, e podia
// adicionar um item que nem aparecia nos resultados. Agora cada item casado
// tem sua própria seção, com seu próprio botão.
func TestSearchAgrupaItensDeNomesDiferentes(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada"}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	// Um botão por item casado — não mais um único botão ambíguo.
	if got := strings.Count(html, `class="watchlist-button"`); got != 3 {
		t.Fatalf(`"watchlist-button" aparece %d vez(es), quero 3 (uma por item casado)`, got)
	}
	for id, name := range map[string]string{"600009": "Espada Primordial", "1147": "Espada Citadina", "4089": "Carta Peixe-Espada"} {
		if got := strings.Count(html, `data-item-id="`+id+`"`); got != 1 {
			t.Errorf("data-item-id=%q (%s) aparece %d vez(es), quero 1", id, name, got)
		}
	}

	// As três linhas de Espada Primordial (itemId 600009, preços diferentes)
	// ficam juntas na mesma seção — nenhum item de nome diferente no meio.
	// Ignora a primeira ocorrência do nome, que é o título "Resultados de
	// Espada Primordial" (nome canônico do primeiro item devolvido pela
	// busca), fora do corpo da tabela.
	body := html[strings.Index(html, "<tbody>"):]
	first := strings.Index(body, "Espada Primordial")
	last := strings.LastIndex(body, "Espada Primordial")
	if first == -1 || last <= first {
		t.Fatalf("não achou as três ocorrências de Espada Primordial no corpo da tabela:\n%s", body)
	}
	section := body[first:last]
	if strings.Contains(section, "Espada Citadina") || strings.Contains(section, "Carta Peixe-Espada") {
		t.Errorf("itens de nomes diferentes aparecem misturados dentro da seção de Espada Primordial:\n%s", section)
	}
}

// TestSearchSemResultados garante que uma busca sem nenhum anúncio nunca
// renderize a tabela de mercado vazia: ela sai do caminho da busca e passa
// para o histórico de preços do item (ver history_test.go).
func TestSearchSemResultados(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"item que ninguém anuncia"}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	wantContains(t, html, "nunca foi vendido no servidor NIDHOGG")
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

// TestSearchOrdenaPorPreco confere que "sort=price" reordena os resultados
// (o fixture "Espada" tem preços bem distintos entre os itens que casam) e
// que o cabeçalho da coluna carrega o link para inverter a direção.
func TestSearchOrdenaPorPreco(t *testing.T) {
	srv, _ := newWebServer(t)

	assertOrder := func(t *testing.T, query string, wantAscByPrice []string) {
		t.Helper()
		_, html := getHTML(t, srv, "/web/search?"+query)
		positions := make([]int, len(wantAscByPrice))
		for i, needle := range wantAscByPrice {
			pos := strings.Index(html, needle)
			if pos == -1 {
				t.Fatalf("HTML não contém %q.\nHTML:\n%s", needle, html)
			}
			positions[i] = pos
		}
		if !sort.IntsAreSorted(positions) {
			t.Errorf("ordem inesperada para %q: posições %v (queria em ordem crescente)", query, positions)
		}
	}

	precosCrescentes := []string{"59.000 z", "4.999.999 z", "129.999.999 z", "158.000.000 z", "299.999.999 z"}
	precosDecrescentes := []string{"299.999.999 z", "158.000.000 z", "129.999.999 z", "4.999.999 z", "59.000 z"}

	t.Run("asc", func(t *testing.T) {
		assertOrder(t, "server=NIDHOGG&item=Espada&sort=price&dir=asc", precosCrescentes)
	})
	t.Run("desc", func(t *testing.T) {
		assertOrder(t, "server=NIDHOGG&item=Espada&sort=price&dir=desc", precosDecrescentes)
	})
	t.Run("sort sem dir usa asc", func(t *testing.T) {
		assertOrder(t, "server=NIDHOGG&item=Espada&sort=price", precosCrescentes)
	})

	// A busca inicial, sem clicar em nenhum cabeçalho, já vem ordenada por
	// preço crescente — é o que faz a seta aparecer de cara, deixando claro
	// que a tabela é ordenável.
	t.Run("busca inicial já vem ordenada por preço crescente", func(t *testing.T) {
		assertOrder(t, "server=NIDHOGG&item=Espada", precosCrescentes)
	})
	_, html := getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")
	wantContains(t, html, "Preço ▲")

	// O cabeçalho de preço aponta para a direção oposta à atual (alterna ao
	// clicar de novo) e mostra a seta correspondente.
	_, html = getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada&sort=price&dir=asc")
	wantContains(t, html,
		`hx-get="/web/search?dir=desc&amp;item=Espada&amp;server=NIDHOGG&amp;sort=price"`,
		"Preço ▲",
	)
	_, html = getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada&sort=price&dir=desc")
	wantContains(t, html,
		`hx-get="/web/search?dir=asc&amp;item=Espada&amp;server=NIDHOGG&amp;sort=price"`,
		"Preço ▼",
	)
}

// TestSearchOrdenaPorQuantidade confere a ordenação pela coluna Qtd, usando
// um mock dedicado já que o fixture padrão não varia ItemCnt entre os itens.
func TestSearchOrdenaPorQuantidade(t *testing.T) {
	mock := gnjoytest.New(gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{
			"Poção": {Items: []gnjoytest.ShopListItem{
				{SvrId: 303, MapId: 835, SSI: "p1", ItemId: 1, ItemName: "Poção", StoreName: "A", ItemPrice: 100, ItemCnt: 50},
				{SvrId: 303, MapId: 835, SSI: "p2", ItemId: 1, ItemName: "Poção", StoreName: "B", ItemPrice: 100, ItemCnt: 5},
				{SvrId: 303, MapId: 835, SSI: "p3", ItemId: 1, ItemName: "Poção", StoreName: "C", ItemPrice: 100, ItemCnt: 999},
			}},
		},
	})
	defer mock.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(mock.URL), gnjoy.WithRateLimit(1000, 1000))
	mux := http.NewServeMux()
	RegisterRoutes(mux, client)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Poção"}, "sort": {"qty"}, "dir": {"asc"}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	posB, posA, posC := strings.Index(html, ">B<"), strings.Index(html, ">A<"), strings.Index(html, ">C<")
	if !(posB < posA && posA < posC) {
		t.Errorf("ordem por qtd asc incorreta: B=%d A=%d C=%d\nHTML:\n%s", posB, posA, posC, html)
	}
	wantContains(t, html, "Qtd ▲")
}

// TestSearchOrdenacaoInvalidaEIgnorada garante que valores desconhecidos de
// "sort"/"dir" não quebram a busca — só caem de volta para a ordem da API.
func TestSearchOrdenacaoInvalidaEIgnorada(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada"}, "sort": {"nome"}, "dir": {"lateral"}}
	resp, html := getHTML(t, srv, "/web/search?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}
	wantContains(t, html, "Espada Primordial")
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
		// O comando /navi mantém a extensão .gat do nome do mapa.
		"/navi prt_mk.gat 120/150",
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
