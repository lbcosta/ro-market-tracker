package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// fakeClock é um relógio controlado pelo teste, seguro para uso concorrente
// (os handlers leem enquanto o teste avança o tempo).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// TestTTLCacheDeduplicaBuscasConcorrentes é a garantia do singleflight:
// várias chamadas simultâneas da mesma chave custam UMA busca, e todas
// recebem o resultado dela.
func TestTTLCacheDeduplicaBuscasConcorrentes(t *testing.T) {
	cache := newTTLCache[int](8)

	var (
		mu      sync.Mutex
		fetches int
	)
	fetch := func() (int, error) {
		mu.Lock()
		fetches++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond) // janela para as concorrentes se acumularem
		return 42, nil
	}

	const n = 10
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := cache.Do("chave", time.Minute, fetch)
			if err != nil || v != 42 {
				t.Errorf("Do = (%d, %v), quero (42, nil)", v, err)
			}
		}()
	}
	wg.Wait()

	if fetches != 1 {
		t.Errorf("houve %d buscas, quero 1 (as concorrentes deveriam compartilhar a primeira)", fetches)
	}
}

// TestTTLCacheValidadePorLeitura: a validade é de cada leitura, não do cache
// — o mesmo resultado pode estar "fresco" para a watchlist (4min) e "velho"
// para uma busca interativa (30s). maxAge zero não aceita cache nenhum.
func TestTTLCacheValidadePorLeitura(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	cache := newTTLCache[int](8)
	cache.now = clock.Now

	fetches := 0
	fetch := func() (int, error) { fetches++; return fetches, nil }

	if v, _ := cache.Do("k", time.Minute, fetch); v != 1 {
		t.Fatalf("primeira leitura = %d, quero 1", v)
	}
	clock.Advance(45 * time.Second)

	// 45s de idade: ainda serve para quem aceita 1min...
	if v, _ := cache.Do("k", time.Minute, fetch); v != 1 {
		t.Errorf("leitura com maxAge 1min = %d, quero 1 (do cache)", v)
	}
	// ...mas não para quem só aceita 30s.
	if v, _ := cache.Do("k", 30*time.Second, fetch); v != 2 {
		t.Errorf("leitura com maxAge 30s = %d, quero 2 (rebuscado)", v)
	}
	// maxAge zero: sempre busca, mesmo com o cache recém-renovado.
	if v, _ := cache.Do("k", 0, fetch); v != 3 {
		t.Errorf("leitura com maxAge 0 = %d, quero 3 (rebuscado)", v)
	}
}

// TestTTLCacheResetDescartaBuscaEmVoo: uma busca disparada ANTES do reset só
// termina depois dele, e o resultado dela não pode reaparecer num cache que
// acabou de ser esvaziado — quem pediu o reset ficaria com um dado de antes,
// com a idade zerada, válido por mais um TTL inteiro.
func TestTTLCacheResetDescartaBuscaEmVoo(t *testing.T) {
	cache := newTTLCache[int](8)

	buscando := make(chan struct{})
	liberar := make(chan struct{})
	buscas := 0

	pronta := make(chan struct{})
	go func() {
		defer close(pronta)
		cache.Do("k", time.Minute, func() (int, error) {
			buscas++
			close(buscando)
			<-liberar
			return 1, nil
		})
	}()

	<-buscando
	cache.reset()
	close(liberar)
	<-pronta

	if v, _ := cache.Do("k", time.Minute, func() (int, error) { buscas++; return 2, nil }); v != 2 {
		t.Errorf("leitura depois do reset = %d, quero 2 (rebuscada): a busca em voo repovoou o cache", v)
	}
	if buscas != 2 {
		t.Errorf("houve %d buscas, quero 2 (a de antes do reset e a de depois)", buscas)
	}
}

func TestTTLCacheDescartaAMaisAntiga(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	cache := newTTLCache[string](2)
	cache.now = clock.Now

	gen := cache.generation()
	cache.putIfCurrent(gen, "a", "primeiro")
	clock.Advance(time.Second)
	cache.putIfCurrent(gen, "b", "segundo")
	clock.Advance(time.Second)
	cache.putIfCurrent(gen, "c", "terceiro") // estoura o tamanho: "a" (o mais antigo) sai

	if _, ok := cache.peek("a"); ok {
		t.Error("\"a\" ainda está no cache, quero que a entrada mais antiga tenha sido descartada")
	}
	for _, k := range []string{"b", "c"} {
		if _, ok := cache.peek(k); !ok {
			t.Errorf("%q sumiu do cache, quero só a mais antiga descartada", k)
		}
	}
}

// TestWatchlistPriceServidoDoCache: uma segunda checagem idêntica logo em
// seguida (outra aba, um F5) não pode custar nada ao upstream — nem a busca
// (cache de resultados), nem o detalhe do refino (memoizado por loja).
func TestWatchlistPriceServidoDoCache(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"600009"}, "item": {"Espada"}}
	getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	after1 := mock.RequestCount()

	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if got := mock.RequestCount(); got != after1 {
		t.Errorf("a segunda checagem custou %d requisições ao upstream, quero 0", got-after1)
	}

	// E a resposta do cache precisa ser a mesma coisa, não um vazio.
	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 129999999 {
		t.Errorf("resultado do cache = %+v, quero o mesmo da primeira checagem", view)
	}
}

// TestWatchlistPriceRefinoMemoizado: o refino de uma loja nunca muda para o
// mesmo ssi, então a segunda checagem com refino fixado não reconsulta os
// detalhes que a primeira já descobriu.
func TestWatchlistPriceRefinoMemoizado(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{
		"server": {"NIDHOGG"}, "itemId": {"600009"},
		"item": {"Espada"}, "refine": {"7"},
	}
	// Primeira checagem: a busca + detalhe do 1º candidato (não bate) +
	// detalhe do 2º (bate) — o caminho caro, pago uma única vez.
	getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if got := mock.RequestCount(); got != 3 {
		t.Fatalf("primeira checagem custou %d requisições, quero 3", got)
	}

	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if got := mock.RequestCount(); got != 3 {
		t.Errorf("a segunda checagem custou %d requisições novas, quero 0 (busca no cache, refinos no memo)", got-3)
	}

	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 158000000 || view.Refine == nil || *view.Refine != 7 {
		t.Errorf("resultado da segunda checagem = %+v, quero o mesmo anúncio +7 da primeira", view)
	}
}

// configComMuitosAnuncios monta um item com mais anúncios do que o orçamento
// de detalhes de uma checagem (maxRefineDetailFetches), com a única unidade
// no refino procurado deliberadamente além do primeiro orçamento — na 11ª
// loja mais barata.
func configComMuitosAnuncios(t *testing.T) gnjoytest.Config {
	t.Helper()
	if maxRefineDetailFetches != 8 {
		t.Fatalf("maxRefineDetailFetches = %d; este teste assume 8 — reveja as contas dele", maxRefineDetailFetches)
	}

	items := make([]gnjoytest.ShopListItem, 0, 12)
	stores := make(map[string]gnjoytest.StoreDetail, 12)
	for i := 1; i <= 12; i++ {
		ssi := fmt.Sprintf("adaga-%d", i)
		items = append(items, gnjoytest.ShopListItem{
			SvrId: 303, MapId: 835, SSI: ssi, ItemId: 777,
			ItemName: "Adaga Rúnica", DatabaseType: "weapon",
			ItemPrice: int64(i) * 1000, ItemCnt: 1,
		})
		fullName := "+0Adaga Rúnica"
		if i == 11 {
			fullName = "+5Adaga Rúnica"
		}
		stores[ssi] = gnjoytest.StoreDetail{
			SvrId: 303, MapId: 835, SSI: ssi, ItemId: 777,
			ItemFullName: fullName, DatabaseType: "weapon",
			ItemPrice: int64(i) * 1000, ItemCnt: 1, MapName: "prt_mk.gat",
		}
	}
	return gnjoytest.Config{
		Searches: map[string]gnjoytest.SearchResult{"Adaga Rúnica": {Items: items}},
		Stores:   stores,
	}
}

// TestWatchlistPriceOrcamentoDeDetalhes cobre o teto de custo do refino
// fixado e a convergência entre ciclos: cada checagem consulta no máximo
// maxRefineDetailFetches lojas NOVAS, e o que ela descobre fica no memo — o
// ciclo seguinte continua de onde ela parou até cobrir todos os anúncios.
func TestWatchlistPriceOrcamentoDeDetalhes(t *testing.T) {
	srv, mock := newWebServerWith(t, configComMuitosAnuncios(t))

	q := url.Values{
		"server": {"NIDHOGG"}, "itemId": {"777"},
		"item": {"Adaga Rúnica"}, "refine": {"5"},
	}

	// 1ª checagem: busca + 8 detalhes (o orçamento) — a unidade +5 está na
	// 11ª loja, fora do alcance deste ciclo, então ainda não foi achada.
	_, body := getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	var view watchlistPriceResponse
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if view.Found {
		t.Errorf("Found = true na primeira checagem, quero false (a unidade +5 está além do orçamento)")
	}
	if got := mock.RequestCount(); got != 9 {
		t.Errorf("primeira checagem custou %d requisições, quero 9 (busca + %d detalhes)", got, maxRefineDetailFetches)
	}

	// 2ª checagem: a busca vem do cache e as 8 primeiras lojas do memo; o
	// orçamento avança pelas lojas novas (9ª, 10ª, 11ª) e acha a unidade +5.
	_, body = getHTML(t, srv, "/web/watchlist/price?"+q.Encode())
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if !view.Found || view.MinPrice != 11000 {
		t.Errorf("segunda checagem = %+v, quero a unidade +5 da 11ª loja (11000)", view)
	}
	if got := mock.RequestCount(); got != 12 {
		t.Errorf("total após a segunda checagem = %d requisições, quero 12 (só os 3 detalhes novos)", got)
	}
}

// TestResetCaches cobre a rota usada pelos testes de navegador para isolar
// um teste do cache que o anterior deixou: depois do reset, a mesma consulta
// volta a ir ao upstream.
func TestResetCaches(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"4089"}, "item": {"Espada"}}
	path := "/web/watchlist/price?" + q.Encode()

	getHTML(t, srv, path)
	getHTML(t, srv, path)
	if got := mock.RequestCount(); got != 1 {
		t.Fatalf("antes do reset: %d requisições, quero 1 (a segunda vem do cache)", got)
	}

	resp, err := srv.Client().Post(srv.URL+"/web/cache/reset", "", nil)
	if err != nil {
		t.Fatalf("POST /web/cache/reset: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status do reset = %d, quero 204", resp.StatusCode)
	}

	getHTML(t, srv, path)
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("depois do reset: %d requisições, quero 2 (o cache foi esvaziado)", got)
	}
}

// TestResetCachesDescartaBuscaEmVoo é a mesma garantia de TestResetCaches
// para a consulta que o reset pega no meio do caminho. Foi a corrida que
// deixou os testes de navegador instáveis: um teste que termina logo depois
// de disparar uma busca a deixa em voo (o servidor não a cancela junto com a
// conexão, de propósito — ver cachedSearchShops), e ela guardava o resultado
// DEPOIS do reset do teste seguinte. Esse teste então recebia a resposta do
// cache, sem chamada nenhuma ao upstream — e ficava esperando para sempre a
// atividade que só uma chamada de verdade publicaria.
func TestResetCachesDescartaBuscaEmVoo(t *testing.T) {
	srv, mock := newWebServer(t)
	path := "/web/search?server=NIDHOGG&item=Espada"

	// Segura a resposta do upstream o bastante para o reset acontecer com a
	// busca ainda em voo, sem depender de acertar uma janela de milissegundos.
	mock.QueueFailure(gnjoytest.Failure{Passthrough: true, Delay: 300 * time.Millisecond}, 1)

	terminou := make(chan struct{})
	go func() {
		defer close(terminou)
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	esperarRequisicaoNoMock(t, mock, 1)
	resp, err := srv.Client().Post(srv.URL+"/web/cache/reset", "", nil)
	if err != nil {
		t.Fatalf("POST /web/cache/reset: %v", err)
	}
	resp.Body.Close()
	<-terminou

	getHTML(t, srv, path)
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("depois do reset: %d requisições, quero 2 (a busca em voo não pode repovoar o cache)", got)
	}
}

// esperarRequisicaoNoMock bloqueia até o mock ter recebido n requisições. O
// mock as registra ao recebê-las, antes de responder, então isto é o gancho
// para agir com uma chamada ainda em voo.
func esperarRequisicaoNoMock(t *testing.T, mock *gnjoytest.Server, n int) {
	t.Helper()
	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		if mock.RequestCount() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("o mock recebeu %d requisições em 5s, esperava %d", mock.RequestCount(), n)
}

// TestResetCachesLimpaAAtividade cobre a mesma motivação de TestResetCaches,
// para o histórico da barra de atividades: sem isto, uma conexão nova ao
// stream (a de um teste de navegador seguinte, depois de recarregar a
// página) veria a última atividade DESTE teste como "a chamada atual" — se a
// atualização da própria ação do teste seguinte demorasse ou se perdesse,
// ele ficaria preso vendo esse resquício.
func TestResetCachesLimpaAAtividade(t *testing.T) {
	srv, _ := newWebServer(t)

	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")

	resp, err := srv.Client().Post(srv.URL+"/web/cache/reset", "", nil)
	if err != nil {
		t.Fatalf("POST /web/cache/reset: %v", err)
	}
	resp.Body.Close()

	events := openActivityStream(t, srv)
	ev := nextEvent(t, events)
	if ev.Name != "snapshot" {
		t.Fatalf("primeiro evento = %q, quero \"snapshot\"", ev.Name)
	}
	var snapshot []activityEventView
	if err := json.Unmarshal([]byte(ev.Data), &snapshot); err != nil {
		t.Fatalf("snapshot inválido: %s", ev.Data)
	}
	if len(snapshot) != 0 {
		t.Errorf("snapshot depois do reset tem %d evento(s), quero 0: a atividade de antes do reset vazou", len(snapshot))
	}
}

// TestSearchReordenacaoServidaDoCache: clicar nos cabeçalhos de ordenação
// refaz o GET /web/search com os mesmos servidor e termo — reordenar é
// trabalho do processo, não motivo para outra ida ao upstream.
func TestSearchReordenacaoServidaDoCache(t *testing.T) {
	srv, mock := newWebServer(t)

	getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada")
	if got := mock.RequestCount(); got != 1 {
		t.Fatalf("a busca custou %d requisições, quero 1", got)
	}

	_, body := getHTML(t, srv, "/web/search?server=NIDHOGG&item=Espada&sort=qty&dir=desc")
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("a reordenação custou %d requisições novas ao upstream, quero 0", got-1)
	}
	wantContains(t, body, "Espada Primordial")
}

// TestWatchlistPriceFreshIgnoraOCache: a checagem automática convive com um
// resultado de até monitorMaxAge, mas o botão "↻" (fresh=1) é o usuário
// pedindo o estado de AGORA — um item recém-anunciado tem de aparecer nele,
// mesmo que a última consulta tenha sido há segundos.
func TestWatchlistPriceFreshIgnoraOCache(t *testing.T) {
	mock := gnjoytest.New(gnjoytest.DemoConfig())
	t.Cleanup(mock.Close)

	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	)
	h := NewHandler(client, "test")
	clock := &fakeClock{t: time.Now()}
	h.searchCache.now = clock.Now

	srv := httptest.NewServer(http.HandlerFunc(h.WatchlistPrice))
	t.Cleanup(srv.Close)

	// Item que não é equipamento: cada consulta custa exatamente 1 busca.
	q := url.Values{"server": {"NIDHOGG"}, "itemId": {"4089"}, "item": {"Espada"}}
	path := "/web/watchlist/price?" + q.Encode()

	getHTML(t, srv, path)
	if got := mock.RequestCount(); got != 1 {
		t.Fatalf("primeira checagem custou %d requisições, quero 1", got)
	}

	// 2 minutos depois (menos que monitorMaxAge): o ciclo automático aceita
	// o cache...
	clock.Advance(2 * time.Minute)
	getHTML(t, srv, path)
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("checagem automática custou %d requisições novas, quero 0 (dentro de monitorMaxAge)", got-1)
	}

	// ...mas o "atualizar agora" não.
	getHTML(t, srv, path+"&fresh=1")
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("fresh=1 custou %d requisições novas, quero 1 (o cache não vale para quem pediu agora)", got-1)
	}

	// E passado o monitorMaxAge, até o ciclo automático rebusca.
	clock.Advance(monitorMaxAge + time.Second)
	getHTML(t, srv, path)
	if got := mock.RequestCount(); got != 3 {
		t.Errorf("checagem após monitorMaxAge custou %d requisições novas, quero 1", got-2)
	}
}
