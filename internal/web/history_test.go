package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// buscarEm pede a busca da página por um termo em um servidor e devolve o
// fragmento HTML renderizado.
func buscarEm(t *testing.T, srv *httptest.Server, server, item string) string {
	t.Helper()
	q := url.Values{"server": {server}, "item": {item}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())
	return html
}

// posicoes devolve, para cada trecho, onde ele aparece no HTML — e falha o
// teste se algum não aparecer.
func posicoes(t *testing.T, html string, trechos ...string) []int {
	t.Helper()
	out := make([]int, len(trechos))
	for i, trecho := range trechos {
		pos := strings.Index(html, trecho)
		if pos == -1 {
			t.Fatalf("HTML não contém %q.\nHTML:\n%s", trecho, html)
		}
		out[i] = pos
	}
	return out
}

// TestHistoricoMostraUmaLinhaPorItem é o caminho feliz da feature e a
// regressão do caso "rapidez": o termo casa dois itens diferentes, e os dois
// precisam aparecer com os números que a API devolveu — sem escolher um só
// nem recalcular agregados por conta própria.
func TestHistoricoMostraUmaLinhaPorItem(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Rapidez")

	wantContains(t, html,
		"Histórico de Rapidez",
		"history-table",
		"Vendas dos últimos 7 dias no servidor NIDHOGG",

		// Automódulo de M-Rapidez: min 5kk, méd 8,66kk, máx 12kk, vol 3.
		"Automódulo de M-Rapidez",
		"5.000.000 z",
		"8.666.666 z",
		"12.000.000 z",
		"<td>3</td>",

		// Módulo de S-Rapidez: min 5kk, méd 7,5kk, máx 10kk, vol 2.
		"Módulo de S-Rapidez",
		"7.500.000 z",
		"10.000.000 z",
		"<td>2</td>",
	)

	if got := strings.Count(html, `class="history-row"`); got != 2 {
		t.Errorf("linhas na tabela = %d, quero 2 (um item por linha)", got)
	}
	if strings.Contains(html, "item-row") {
		t.Error("não deveria haver linhas de anúncio para um item fora do mercado")
	}
}

// TestHistoricoPreservaAOrdemDaAPI garante que a lista sai como o site a
// devolveu (ele já ordena por relevância) em vez de ser reordenada aqui.
func TestHistoricoPreservaAOrdemDaAPI(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Rapidez")

	pos := posicoes(t, html, "Automódulo de M-Rapidez", "Módulo de S-Rapidez")
	if pos[0] > pos[1] {
		t.Errorf("a ordem da API não foi preservada: posições %v", pos)
	}
}

// TestHistoricoTemBotaoDeWatchlistPorLinha: como a busca casa vários itens,
// só quem procurou sabe qual deles quer rastrear — daí um botão por linha, e
// não um só no cabeçalho.
func TestHistoricoTemBotaoDeWatchlistPorLinha(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Rapidez")

	wantContains(t, html,
		`data-item-id="1000125" data-item-name="Automódulo de M-Rapidez"`,
		`data-item-id="25690" data-item-name="Módulo de S-Rapidez"`,
		`data-server="NIDHOGG"`,
	)
	if got := strings.Count(html, "addToWatchlist(this)"); got != 2 {
		t.Errorf("botões de watchlist = %d, quero 2 (um por linha)", got)
	}

	// Quem chega por aqui não está esperando um preço: o item não está à
	// venda, e o que se espera é ele voltar ao mercado. É o data-mode que diz
	// isso à watchlist (ver MODE_AVAILABILITY em watchlist.js).
	if got := strings.Count(html, `data-mode="availability"`); got != 2 {
		t.Errorf("botões em modo de disponibilidade = %d, quero 2", got)
	}
}

// periodosConsultados devolve, na ordem, os períodos das consultas de preços
// praticados que chegaram ao mock.
func periodosConsultados(mock *gnjoytest.Server) []string {
	var periodos []string
	for _, req := range mock.Requests() {
		if strings.HasSuffix(req.Path, "/market-price") {
			periodos = append(periodos, req.Query.Get("period"))
		}
	}
	return periodos
}

// TestHistoricoConsultaAsDuasJanelas trava os parâmetros e a ordem das
// consultas. São duas porque elas respondem coisas diferentes: o histórico
// completo diz quais itens existem, e a janela de 7 dias diz quanto eles
// custam hoje — e é a segunda que ganha quando o item vendeu nas duas.
func TestHistoricoConsultaAsDuasJanelas(t *testing.T) {
	srv, mock := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Rapidez")

	if got := periodosConsultados(mock); strings.Join(got, ",") != "ALL,7" {
		t.Errorf("períodos consultados = %v, quero [ALL 7] (a lista completa primeiro)", got)
	}
	for _, req := range mock.Requests() {
		if !strings.HasSuffix(req.Path, "/market-price") {
			continue
		}
		if got := req.Query.Get("searchWord"); got != "Rapidez" {
			t.Errorf("searchWord = %q, quero \"Rapidez\"", got)
		}
		if got := req.Query.Get("serverType"); got != "NIDHOGG" {
			t.Errorf("serverType = %q, quero \"NIDHOGG\"", got)
		}
	}

	// Os dois itens venderam na última semana, então são os números dessa
	// janela que aparecem — não os do histórico completo, mais largos.
	wantContains(t, html, "8.666.666 z")
	if strings.Contains(html, "20.000.000 z") {
		t.Errorf("os números do histórico completo não deveriam aparecer quando há venda recente:\n%s", html)
	}
	// Com todas as linhas na mesma janela, a coluna de período não acrescenta
	// nada e não deve ser renderizada.
	if strings.Contains(html, "history-period") {
		t.Error("a coluna de período só deveria aparecer quando as linhas vêm de janelas diferentes")
	}
}

// TestHistoricoNaoPerdeItemSemVendaRecente é a regressão do caso "reformador
// primordial ii": buscar por ele mostrava só o "III". O "II" existe e tem
// histórico, mas não vendeu na última semana — e a tabela era montada a
// partir dessa janela, então ele sumia sem o usuário ter como saber que
// existe.
func TestHistoricoNaoPerdeItemSemVendaRecente(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Reformador Primordial")

	if got := strings.Count(html, `class="history-row"`); got != 2 {
		t.Fatalf("linhas na tabela = %d, quero 2 (o II não pode sumir por não ter vendido na semana)\nHTML:\n%s", got, html)
	}
	wantContains(t, html,
		"Reformador Primordial III",
		"Reformador Primordial II",

		// O "III" vendeu na última semana: números dessa janela.
		"129.999.988 z",
		// O "II" não vendeu: números de todo o histórico, e é por isso que a
		// linha precisa dizer de qual janela ela veio.
		"109.999.998 z",
		"500.000.000 z",

		"history-period",
		"todo o histórico",
	)
}

// TestHistoricoCaiParaOHistoricoCompleto cobre o item que já foi vendido,
// mas não na última semana: a janela de 7 dias vem vazia e a resposta útil é
// o histórico completo, deixando claro que a janela mudou.
func TestHistoricoCaiParaOHistoricoCompleto(t *testing.T) {
	srv, mock := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Bota do Andarilho")

	wantContains(t, html,
		"nenhuma venda nos últimos 7 dias",
		"todo o histórico de vendas no servidor NIDHOGG",
		"Bota do Andarilho",
		"800.000 z",
		"1.250.000 z",
		"2.000.000 z",
		"<td>14</td>",
	)

	if got := periodosConsultados(mock); strings.Join(got, ",") != "ALL,7" {
		t.Errorf("períodos consultados = %v, quero [ALL 7]", got)
	}
	// Todas as linhas na mesma janela: o texto acima da tabela já diz qual é,
	// e a coluna por linha seria só ruído.
	if strings.Contains(html, "history-period") {
		t.Error("a coluna de período não deveria aparecer com todas as linhas na mesma janela")
	}
}

// TestHistoricoDeItemNuncaVendido cobre o outro desfecho: nem na última
// semana nem no histórico completo há venda desse item no servidor.
func TestHistoricoDeItemNuncaVendido(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", "Elmo Ancestral")

	wantContains(t, html, "nunca foi vendido no servidor NIDHOGG", "Elmo Ancestral")
	if strings.Contains(html, "results-table") {
		t.Errorf("não deveria haver tabela para um item sem histórico:\n%s", html)
	}
}

// TestHistoricoErroNoHistoricoCompleto: sem a lista completa não há tabela a
// montar, e uma falha é diferente de "nunca foi vendido", que é uma resposta
// legítima do site.
func TestHistoricoErroNoHistoricoCompleto(t *testing.T) {
	srv, mock := newWebServer(t)

	// A busca no mercado passa (não acha nada) e a primeira consulta de
	// preços praticados, a do histórico completo, falha.
	mock.QueueFailure(gnjoytest.Failure{Passthrough: true}, 1)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 20)

	resp, html := getHTML(t, srv, "/web/search?server=NIDHOGG&item=Rapidez")

	// O fragmento é trocado no DOM pelo HTMX, então precisa vir com 200 e a
	// mensagem de erro dentro.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, quero 200 (o HTMX precisa trocar o fragmento)", resp.StatusCode)
	}
	wantContains(t, html, `class="error"`, "Não foi possível consultar o histórico de preços agora.")
	if strings.Contains(html, "history-table") {
		t.Errorf("não deveria haver tabela quando a consulta falha:\n%s", html)
	}
}

// TestHistoricoSegueSemAJanelaRecente: falhar a segunda consulta não custa a
// tabela inteira. A lista completa já está em mãos, e cada linha diz de qual
// janela vieram seus números — mostrar o histórico completo é bem melhor que
// um "não foi possível consultar" com os dados na mão.
func TestHistoricoSegueSemAJanelaRecente(t *testing.T) {
	srv, mock := newWebServer(t)

	// Passam a busca no mercado e o histórico completo; falha só a janela de
	// 7 dias e as repetições que o client tentar depois dela.
	mock.QueueFailure(gnjoytest.Failure{Passthrough: true}, 2)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 20)

	_, html := getHTML(t, srv, "/web/search?server=NIDHOGG&item=Rapidez")

	if strings.Contains(html, `class="error"`) {
		t.Errorf("a tabela deveria sair mesmo sem a janela recente:\n%s", html)
	}
	wantContains(t, html,
		"nenhuma venda nos últimos 7 dias",
		// Os números do histórico completo, já que a janela recente não veio.
		"20.000.000 z",
	)
	if got := strings.Count(html, `class="history-row"`); got != 2 {
		t.Errorf("linhas na tabela = %d, quero 2", got)
	}
}

// TestHistoricoEscapaHTML garante que o termo digitado, que volta no título e
// na mensagem, seja exibido como texto e não interpretado.
func TestHistoricoEscapaHTML(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarEm(t, srv, "NIDHOGG", `<script>alert("xss")</script>`)

	if strings.Contains(html, "<script>alert") {
		t.Errorf("o termo buscado foi injetado sem escape:\n%s", html)
	}
	wantContains(t, html, "&lt;script&gt;")
}

// TestHistoricoEscapaNomeDeItem cobre o outro texto que vem de fora: o nome
// do item devolvido pela API, que também alimenta o atributo do botão da
// watchlist.
func TestHistoricoEscapaNomeDeItem(t *testing.T) {
	suspeito := gnjoytest.MarketPriceResult{Items: []gnjoytest.MarketPriceItem{{
		SvrId: 303, ItemId: 1, ItemName: `<img src=x onerror="alert(1)">`,
		TotalItemCnt: 1, MinItemPrice: 1, AvgItemPrice: 1, MaxItemPrice: 1,
	}}}
	srv, _ := newWebServerWith(t, gnjoytest.Config{
		MarketPrices: map[gnjoytest.MarketPriceScope]gnjoytest.MarketPriceResult{
			{ServerType: "NIDHOGG", SearchWord: "Suspeito", Period: "ALL"}: suspeito,
			{ServerType: "NIDHOGG", SearchWord: "Suspeito", Period: "7"}:   suspeito,
		},
	})

	html := buscarEm(t, srv, "NIDHOGG", "Suspeito")

	if strings.Contains(html, "<img src=x") {
		t.Errorf("o nome do item foi injetado sem escape:\n%s", html)
	}
	wantContains(t, html, "&lt;img")
}
