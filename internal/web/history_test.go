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

// TestHistoricoConsultaOsUltimos7Dias trava os parâmetros da consulta: é o
// período de 7 dias que produz os números que o site mostra na página de
// preços de mercado.
func TestHistoricoConsultaOsUltimos7Dias(t *testing.T) {
	srv, mock := newWebServer(t)

	buscarEm(t, srv, "NIDHOGG", "Rapidez")

	var consultas []url.Values
	for _, req := range mock.Requests() {
		if strings.HasSuffix(req.Path, "/market-price") {
			consultas = append(consultas, req.Query)
		}
	}
	if len(consultas) != 1 {
		t.Fatalf("consultas de preços praticados = %d, quero 1 (a janela de 7 dias já tinha o que mostrar)", len(consultas))
	}
	q := consultas[0]
	if got := q.Get("period"); got != "7" {
		t.Errorf("period = %q, quero \"7\"", got)
	}
	if got := q.Get("searchWord"); got != "Rapidez" {
		t.Errorf("searchWord = %q, quero \"Rapidez\"", got)
	}
	if got := q.Get("serverType"); got != "NIDHOGG" {
		t.Errorf("serverType = %q, quero \"NIDHOGG\"", got)
	}
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

	var periodos []string
	for _, req := range mock.Requests() {
		if strings.HasSuffix(req.Path, "/market-price") {
			periodos = append(periodos, req.Query.Get("period"))
		}
	}
	if strings.Join(periodos, ",") != "7,ALL" {
		t.Errorf("períodos consultados = %v, quero [7 ALL]", periodos)
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

// TestHistoricoErroDoUpstream: uma falha na consulta é diferente de "nunca
// foi vendido", que é uma resposta legítima do site.
func TestHistoricoErroDoUpstream(t *testing.T) {
	tests := []struct {
		name         string
		item         string
		passthroughs int
	}{
		// A busca no mercado passa (não acha nada) e a consulta da janela de
		// 7 dias falha.
		{"janela de 7 dias", "Rapidez", 1},
		// A janela de 7 dias passa (vem vazia) e o histórico completo falha.
		{"histórico completo", "Bota do Andarilho", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, mock := newWebServer(t)
			mock.QueueFailure(gnjoytest.Failure{Passthrough: true}, tt.passthroughs)
			mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 20)

			resp, html := getHTML(t, srv, "/web/search?server=NIDHOGG&item="+url.QueryEscape(tt.item))

			// O fragmento é trocado no DOM pelo HTMX, então precisa vir com
			// 200 e a mensagem de erro dentro.
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, quero 200 (o HTMX precisa trocar o fragmento)", resp.StatusCode)
			}
			wantContains(t, html, `class="error"`, "Não foi possível consultar o histórico de preços agora.")
			if strings.Contains(html, "history-table") {
				t.Errorf("não deveria haver tabela quando a consulta falha:\n%s", html)
			}
		})
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
	srv, _ := newWebServerWith(t, gnjoytest.Config{
		MarketPrices: map[gnjoytest.MarketPriceScope]gnjoytest.MarketPriceResult{
			{ServerType: "NIDHOGG", SearchWord: "Suspeito", Period: "7"}: {Items: []gnjoytest.MarketPriceItem{{
				SvrId: 303, ItemId: 1, ItemName: `<img src=x onerror="alert(1)">`,
				TotalItemCnt: 1, MinItemPrice: 1, AvgItemPrice: 1, MaxItemPrice: 1,
			}}},
		},
	})

	html := buscarEm(t, srv, "NIDHOGG", "Suspeito")

	if strings.Contains(html, "<img src=x") {
		t.Errorf("o nome do item foi injetado sem escape:\n%s", html)
	}
	wantContains(t, html, "&lt;img")
}
