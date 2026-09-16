package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// validar faz a chamada e devolve o status junto do corpo já decodificado.
// Os testes olham os dois: o status é o que separa "não achei" de "não
// consegui perguntar", e essa distinção é o ponto mais delicado da rota.
func validar(t *testing.T, srv *httptest.Server, server, item string) (int, validarView) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/web/estoque/validar?server=" + server + "&item=" + item)
	if err != nil {
		t.Fatalf("GET /web/estoque/validar: %v", err)
	}
	defer resp.Body.Close()

	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lendo corpo: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, validarView{}
	}

	var view validarView
	if err := json.Unmarshal(corpo, &view); err != nil {
		t.Fatalf("decodificando %q: %v", corpo, err)
	}
	return resp.StatusCode, view
}

// TestValidarAchaNoMercado cobre o caminho comum: alguém está anunciando o
// item, e uma requisição basta. "Espada" casa três itens diferentes, então
// serve também para conferir o agrupamento — a busca devolve uma linha por
// anúncio, e três lojas vendendo a mesma espada são UM candidato.
func TestValidarAchaNoMercado(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	status, view := validar(t, srv, "NIDHOGG", "Espada")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200", status)
	}
	if view.Message != "" {
		t.Errorf("Message = %q, quero vazio quando houve candidato", view.Message)
	}

	porID := map[int]candidatoView{}
	for _, c := range view.Candidates {
		porID[c.ItemID] = c
	}

	primordial, ok := porID[600009]
	if !ok {
		t.Fatalf("candidatos não trazem a Espada Primordial (600009): %+v", view.Candidates)
	}
	if !primordial.InMarket {
		t.Error("InMarket = false, mas o item veio da busca de anúncios")
	}
	// Três anúncios na fixture: 129.999.999, 158.000.000 e 299.999.999, com
	// uma unidade cada.
	if primordial.MinPrice != 129999999 {
		t.Errorf("MinPrice = %d, quero 129999999 (o mais barato dos três anúncios)", primordial.MinPrice)
	}
	if primordial.Units != 3 {
		t.Errorf("Units = %d, quero 3 (a soma das unidades dos três anúncios)", primordial.Units)
	}
	if primordial.SvrID == 0 {
		t.Error("SvrID = 0; o histórico de preço exige o id numérico do servidor")
	}

	// Uma requisição, não duas: com o item no mercado, o histórico não é
	// consultado. É a economia que a ordem das duas consultas existe para dar.
	if n := mock.RequestCount(); n != 1 {
		t.Errorf("requisições ao upstream = %d, quero 1 (o histórico não deveria ter sido consultado)", n)
	}
}

// TestValidarCaiNoHistorico é a rede de segurança: ninguém está anunciando o
// item agora, o que NÃO quer dizer que ele não existe — e é justamente o caso
// de quem está prestes a colocá-lo à venda.
func TestValidarCaiNoHistorico(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	status, view := validar(t, srv, "NIDHOGG", "Bota+do+Andarilho")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200", status)
	}
	if len(view.Candidates) != 1 {
		t.Fatalf("candidatos = %d, quero 1: %+v", len(view.Candidates), view.Candidates)
	}

	c := view.Candidates[0]
	if c.InMarket {
		t.Error("InMarket = true, mas o item só foi achado no histórico")
	}
	if c.ItemID != 610003 {
		t.Errorf("ItemID = %d, quero 610003", c.ItemID)
	}
	if c.Min != 800000 || c.Avg != 1250000 || c.Max != 2000000 || c.Vol != 14 {
		t.Errorf("agregados do histórico = %d/%d/%d vol %d, quero 800000/1250000/2000000 vol 14",
			c.Min, c.Avg, c.Max, c.Vol)
	}

	// Duas: a busca no mercado (vazia) e o histórico.
	if n := mock.RequestCount(); n != 2 {
		t.Errorf("requisições ao upstream = %d, quero 2", n)
	}
}

// TestValidarDesambigua garante que um nome que casa vários itens devolve
// todos, em ordem estável — quem decide qual é o dele é o usuário.
func TestValidarDesambigua(t *testing.T) {
	srv, _ := newWebServer(t)

	_, view := validar(t, srv, "NIDHOGG", "Rapidez")
	if len(view.Candidates) != 2 {
		t.Fatalf("candidatos = %d, quero 2: %+v", len(view.Candidates), view.Candidates)
	}

	// Ordenado por nome: "Automódulo de M-Rapidez" antes de "Módulo de
	// S-Rapidez". Iterar um map em Go tem ordem aleatória, e uma lista de
	// escolha que troca de ordem a cada validação seria hostil.
	if view.Candidates[0].ItemName != "Automódulo de M-Rapidez" {
		t.Errorf("primeiro candidato = %q, quero \"Automódulo de M-Rapidez\"", view.Candidates[0].ItemName)
	}
	if view.Candidates[1].ItemName != "Módulo de S-Rapidez" {
		t.Errorf("segundo candidato = %q, quero \"Módulo de S-Rapidez\"", view.Candidates[1].ItemName)
	}
}

// TestValidarNomeComSlotsVemDoMercado cobre o que a ordem das consultas
// entrega de graça: só o anúncio traz o sufixo de slots, e é ele que separa
// dois itens de catálogo que, pelo histórico, chegariam com o mesmo nome.
func TestValidarNomeComSlotsVemDoMercado(t *testing.T) {
	srv, _ := newWebServer(t)

	_, view := validar(t, srv, "NIDHOGG", "Selo+de+Loki")
	if len(view.Candidates) != 2 {
		t.Fatalf("candidatos = %d, quero 2: %+v", len(view.Candidates), view.Candidates)
	}

	nomes := []string{view.Candidates[0].ItemName, view.Candidates[1].ItemName}
	if nomes[0] == nomes[1] {
		t.Fatalf("os dois candidatos vieram com o mesmo nome (%q); o sufixo de slots deveria separá-los", nomes[0])
	}
	comSlot := view.Candidates[0]
	if !strings.Contains(comSlot.ItemName, "[") {
		comSlot = view.Candidates[1]
	}
	if !strings.Contains(comSlot.ItemName, "[") {
		t.Fatalf("nenhum candidato veio com sufixo de slots: %v", nomes)
	}
	// O SearchName é o que volta ao upstream, e lá o sufixo não existe.
	if strings.Contains(comSlot.SearchName, "[") {
		t.Errorf("SearchName = %q, não deveria levar o sufixo de slots", comSlot.SearchName)
	}
}

// TestValidarItemInexistente é o único caminho que autoriza o navegador a
// marcar o item como inválido: as DUAS consultas vieram vazias.
func TestValidarItemInexistente(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	status, view := validar(t, srv, "NIDHOGG", "Item+Que+Nao+Existe")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200 — não achar o item não é erro da requisição", status)
	}
	if len(view.Candidates) != 0 {
		t.Errorf("candidatos = %+v, quero nenhum", view.Candidates)
	}
	if view.Message == "" {
		t.Error("Message vazia; o navegador precisa do motivo para mostrar no card")
	}
	if n := mock.RequestCount(); n != 2 {
		t.Errorf("requisições ao upstream = %d, quero 2 (mercado e histórico)", n)
	}
}

// TestValidarFalhaNaoInvalidaOItem é a asserção mais importante da rota. Um
// tropeço de rede não pode virar "este item não existe": esse é o estado que
// manda o usuário apagar o cadastro, e apagá-lo por causa de um timeout
// destruiria um cadastro correto.
func TestValidarFalhaNaoInvalidaOItem(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	resp, err := srv.Client().Get(srv.URL + "/web/estoque/validar?server=NIDHOGG&item=Espada")
	if err != nil {
		t.Fatalf("GET /web/estoque/validar: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, quero 502 — a resposta precisa ser distinguível de uma lista vazia", resp.StatusCode)
	}
	corpo, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(corpo)) == "" {
		t.Error("corpo vazio; o navegador mostra esta mensagem no toast")
	}
}

func TestValidarParametrosObrigatorios(t *testing.T) {
	srv, _ := newWebServer(t)

	for _, query := range []string{"", "?server=NIDHOGG", "?item=Espada"} {
		resp, err := srv.Client().Get(srv.URL + "/web/estoque/validar" + query)
		if err != nil {
			t.Fatalf("GET %s: %v", query, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status de %q = %d, quero 400", query, resp.StatusCode)
		}
	}
}

// TestValidarUsaOCache confirma que revalidar não custa outra ida ao site —
// tentar de novo depois de corrigir o nome é barato de propósito.
func TestValidarUsaOCache(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	validar(t, srv, "NIDHOGG", "Espada")
	validar(t, srv, "NIDHOGG", "Espada")

	if n := mock.RequestCount(); n != 1 {
		t.Errorf("requisições ao upstream = %d, quero 1 (a segunda validação sai do cache)", n)
	}
}
