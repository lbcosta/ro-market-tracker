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

// ---------------------------------------------------------------------------
// Mercado
// ---------------------------------------------------------------------------

func consultarMercado(t *testing.T, srv *httptest.Server, query string) (int, mercadoView) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/web/estoque/mercado?" + query)
	if err != nil {
		t.Fatalf("GET /web/estoque/mercado: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, mercadoView{}
	}
	var view mercadoView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	return resp.StatusCode, view
}

// TestMercadoDevolveAnunciosOrdenados cobre o contrato que o navegador
// depende: os anúncios vêm do mais barato para o mais caro, com o vendedor de
// cada um, para o cliente conseguir separar os seus dos da concorrência.
func TestMercadoDevolveAnunciosOrdenados(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	status, view := consultarMercado(t, srv, "server=NIDHOGG&itemId=600009&item=Espada+Primordial")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200", status)
	}
	if !view.Found {
		t.Fatal("Found = false, mas a fixture tem três anúncios da Espada Primordial")
	}
	if len(view.Listings) != 3 {
		t.Fatalf("anúncios = %d, quero 3: %+v", len(view.Listings), view.Listings)
	}
	if view.DisplayName != "Espada Primordial" {
		t.Errorf("DisplayName = %q, quero \"Espada Primordial\"", view.DisplayName)
	}

	for i := 1; i < len(view.Listings); i++ {
		if view.Listings[i-1].Price > view.Listings[i].Price {
			t.Fatalf("anúncios fora de ordem: %+v", view.Listings)
		}
	}
	if view.Listings[0].Price != 129999999 {
		t.Errorf("primeiro preço = %d, quero 129999999", view.Listings[0].Price)
	}
	// O vendedor é o que torna o desconto dos anúncios próprios possível sem
	// requisição extra — sem ele, a feature inteira precisaria do detalhe de
	// cada loja.
	for _, a := range view.Listings {
		if a.Seller == "" {
			t.Errorf("anúncio sem vendedor: %+v", a)
		}
		if a.StoreName == "" {
			t.Errorf("anúncio sem nome de loja: %+v", a)
		}
	}

	if n := mock.RequestCount(); n != 1 {
		t.Errorf("requisições ao upstream = %d, quero 1", n)
	}
}

// TestMercadoNaoConsultaDetalheDaLoja trava a economia desta rota sobre a da
// watchlist: lá, cada consulta gasta uma requisição extra de GetStoreDetail
// só para obter o /navi. Quem tem o item no estoque não vai a lugar nenhum —
// vai comparar preço —, e o nome da loja já vem de graça na busca.
func TestMercadoNaoConsultaDetalheDaLoja(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	consultarMercado(t, srv, "server=NIDHOGG&itemId=600009&item=Espada+Primordial")

	for _, req := range mock.Requests() {
		if strings.Contains(req.Body, "\"store\"") {
			t.Errorf("a rota consultou o detalhe de uma loja: %s", req.Body)
		}
	}
}

// TestMercadoFiltraPeloItemId garante que uma busca que casa vários itens não
// contamina o card de um deles. "Espada" traz três itens diferentes.
func TestMercadoFiltraPeloItemId(t *testing.T) {
	srv, _ := newWebServer(t)

	_, view := consultarMercado(t, srv, "server=NIDHOGG&itemId=600009&item=Espada")
	if len(view.Listings) != 3 {
		t.Fatalf("anúncios = %d, quero só os 3 da Espada Primordial: %+v", len(view.Listings), view.Listings)
	}
}

// TestMercadoSemAnuncio cobre o item validado pelo histórico: ele existe, mas
// ninguém está vendendo agora. Não é erro, e o card precisa saber a
// diferença.
func TestMercadoSemAnuncio(t *testing.T) {
	srv, _ := newWebServer(t)

	status, view := consultarMercado(t, srv, "server=NIDHOGG&itemId=610003&item=Bota+do+Andarilho")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200", status)
	}
	if view.Found {
		t.Error("Found = true, mas ninguém anuncia a Bota do Andarilho")
	}
	if len(view.Listings) != 0 {
		t.Errorf("anúncios = %+v, quero nenhum", view.Listings)
	}
}

func TestMercadoUsaOCacheEFreshOIgnora(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	q := "server=NIDHOGG&itemId=600009&item=Espada+Primordial"
	consultarMercado(t, srv, q)
	consultarMercado(t, srv, q)
	if n := mock.RequestCount(); n != 1 {
		t.Fatalf("requisições = %d, quero 1 (a segunda sai do cache)", n)
	}

	consultarMercado(t, srv, q+"&fresh=1")
	if n := mock.RequestCount(); n != 2 {
		t.Errorf("requisições = %d, quero 2 (fresh=1 ignora o cache)", n)
	}
}

func TestMercadoParametrosObrigatorios(t *testing.T) {
	srv, _ := newWebServer(t)

	casos := []string{
		"itemId=600009&item=Espada",
		"server=NIDHOGG&item=Espada",
		"server=NIDHOGG&itemId=600009",
		"server=NIDHOGG&itemId=abc&item=Espada",
	}
	for _, q := range casos {
		resp, err := srv.Client().Get(srv.URL + "/web/estoque/mercado?" + q)
		if err != nil {
			t.Fatalf("GET %s: %v", q, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status de %q = %d, quero 400", q, resp.StatusCode)
		}
	}
}

func TestMercadoFalhaRespondeErro(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	resp, err := srv.Client().Get(
		srv.URL + "/web/estoque/mercado?server=NIDHOGG&itemId=600009&item=Espada+Primordial")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, quero 502", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Histórico
// ---------------------------------------------------------------------------
//
// O item 700001 ("Elixir do Mercador") tem 32 dias de histórico numa
// progressão: preço médio 1000 + i*10 do mais recente para o mais antigo, uma
// unidade vendida por dia. Com isso, a quantidade vendida de uma janela É o
// número de dias dela, e os agregados saem exatos sem reproduzir a fórmula de
// stats.go aqui.

func consultarHistorico(t *testing.T, srv *httptest.Server, query string) (int, historicoView) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/web/estoque/historico?" + query)
	if err != nil {
		t.Fatalf("GET /web/estoque/historico: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, historicoView{}
	}
	var view historicoView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	return resp.StatusCode, view
}

// TestHistoricoJanelaRecorta é o teste que só passou a significar alguma coisa
// depois de o mock honrar o "limit": antes, as quatro janelas devolviam a
// série inteira e qualquer implementação passaria.
func TestHistoricoJanelaRecorta(t *testing.T) {
	srv, _ := newWebServer(t)

	casos := []struct {
		janela string
		dias   int
	}{
		{janelaDia, 1},
		{janelaSete, 7},
		{janelaMes, 30},
	}
	for _, caso := range casos {
		t.Run(caso.janela, func(t *testing.T) {
			status, view := consultarHistorico(t, srv,
				"itemId=700001&svrId=303&janela="+caso.janela)
			if status != http.StatusOK {
				t.Fatalf("status = %d, quero 200", status)
			}
			if len(view.Days) != caso.dias {
				t.Errorf("dias = %d, quero %d", len(view.Days), caso.dias)
			}
			if view.Summary.Days != caso.dias {
				t.Errorf("resumo.Days = %d, quero %d", view.Summary.Days, caso.dias)
			}
			// Uma unidade por dia na fixture.
			if view.Summary.QtySold != caso.dias {
				t.Errorf("QtySold = %d, quero %d", view.Summary.QtySold, caso.dias)
			}
			// O total de dias conhecidos NÃO é o tamanho da janela — é o que
			// permite ao card dizer "7 de 32".
			if view.DaysAvailable != 32 {
				t.Errorf("DaysAvailable = %d, quero 32", view.DaysAvailable)
			}
			if view.Window != caso.janela {
				t.Errorf("Window = %q, quero %q", view.Window, caso.janela)
			}
		})
	}
}

// TestHistoricoTudoUsaOTotalDisponivel cobre a janela que não tem um número
// fixo: quantos dias existem só se descobre perguntando.
func TestHistoricoTudoUsaOTotalDisponivel(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	status, view := consultarHistorico(t, srv, "itemId=700001&svrId=303&janela=ALL")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200", status)
	}
	if len(view.Days) != 32 {
		t.Fatalf("dias = %d, quero os 32 da série inteira", len(view.Days))
	}
	if view.DaysAvailable != 32 {
		t.Errorf("DaysAvailable = %d, quero 32", view.DaysAvailable)
	}
	// Duas: a sonda (que descobre o total) e a busca do total. A fixture tem
	// mais dias que a sonda, então este é o caminho de duas requisições.
	if n := mock.RequestCount(); n != 2 {
		t.Errorf("requisições = %d, quero 2 (sonda + total)", n)
	}
}

// TestHistoricoTudoNaoGastaSegundaConsulta: quando o item tem menos dias que a
// sonda, ela já é a resposta completa.
func TestHistoricoTudoNaoGastaSegundaConsulta(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	// 600009 tem 3 dias.
	_, view := consultarHistorico(t, srv, "itemId=600009&svrId=303&janela=ALL")
	if len(view.Days) != 3 {
		t.Fatalf("dias = %d, quero 3", len(view.Days))
	}
	if n := mock.RequestCount(); n != 1 {
		t.Errorf("requisições = %d, quero 1 (a sonda já trouxe tudo)", n)
	}
}

// TestHistoricoAgregadosBatem confere os números do resumo contra a fixture,
// para o card não mostrar estatística errada em silêncio.
func TestHistoricoAgregadosBatem(t *testing.T) {
	srv, _ := newWebServer(t)

	_, view := consultarHistorico(t, srv, "itemId=700001&svrId=303&janela=7")

	// Os 7 dias mais recentes: médias 1000, 1010, ... 1060; mínimos e máximos
	// a 100 de distância; uma unidade por dia.
	if view.Days[0].Avg != 1000 || view.Days[6].Avg != 1060 {
		t.Errorf("médias das pontas = %d e %d, quero 1000 e 1060", view.Days[0].Avg, view.Days[6].Avg)
	}
	if view.Summary.Min != 900 {
		t.Errorf("Min = %d, quero 900", view.Summary.Min)
	}
	if view.Summary.Max != 1160 {
		t.Errorf("Max = %d, quero 1160", view.Summary.Max)
	}
	// Média ponderada por ItemCnt, que é 1 em todos: a média simples de
	// 1000..1060 é 1030.
	if view.Summary.WeightedAvg != 1030 {
		t.Errorf("WeightedAvg = %v, quero 1030", view.Summary.WeightedAvg)
	}
}

func TestHistoricoSemVendas(t *testing.T) {
	srv, _ := newWebServer(t)

	// 4005 está semeado com histórico vazio.
	status, view := consultarHistorico(t, srv, "itemId=4005&svrId=303&janela=7")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quero 200 — não ter venda não é erro", status)
	}
	if len(view.Days) != 0 {
		t.Errorf("dias = %+v, quero nenhum", view.Days)
	}
	if view.DaysAvailable != 0 {
		t.Errorf("DaysAvailable = %d, quero 0", view.DaysAvailable)
	}
}

func TestHistoricoUsaOCacheEFreshOIgnora(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.ResetRequests()

	q := "itemId=700001&svrId=303&janela=7"
	consultarHistorico(t, srv, q)
	consultarHistorico(t, srv, q)
	if n := mock.RequestCount(); n != 1 {
		t.Fatalf("requisições = %d, quero 1 (a segunda sai do cache)", n)
	}

	// Janela diferente é chave diferente: o site pagina a série pelo limit,
	// então a resposta de 30 dias não está contida na de 7.
	consultarHistorico(t, srv, "itemId=700001&svrId=303&janela=30")
	if n := mock.RequestCount(); n != 2 {
		t.Fatalf("requisições = %d, quero 2 (a janela de 30 é outra consulta)", n)
	}

	consultarHistorico(t, srv, q+"&fresh=1")
	if n := mock.RequestCount(); n != 3 {
		t.Errorf("requisições = %d, quero 3 (fresh=1 ignora o cache)", n)
	}
}

func TestHistoricoParametrosObrigatorios(t *testing.T) {
	srv, _ := newWebServer(t)

	casos := []string{
		"svrId=303&janela=7",
		"itemId=700001&janela=7",
		"itemId=700001&svrId=303",
		"itemId=700001&svrId=303&janela=90",
		"itemId=700001&svrId=303&janela=tudo",
	}
	for _, q := range casos {
		resp, err := srv.Client().Get(srv.URL + "/web/estoque/historico?" + q)
		if err != nil {
			t.Fatalf("GET %s: %v", q, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status de %q = %d, quero 400", q, resp.StatusCode)
		}
	}
}

func TestHistoricoFalhaRespondeErro(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	resp, err := srv.Client().Get(srv.URL + "/web/estoque/historico?itemId=700001&svrId=303&janela=7")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, quero 502", resp.StatusCode)
	}
}
