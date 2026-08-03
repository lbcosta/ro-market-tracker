package gnjoytest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultActionID é o hash da Server Action servido pelo mock quando a
	// Config não define um. É hexadecimal e tem tamanho compatível com o que
	// o descobridor de action id procura nos chunks.
	DefaultActionID = "0123456789abcdef0123456789abcdef01234567"

	// DefaultLocale é o segmento de idioma das rotas servidas pelo mock.
	DefaultLocale = "pt"

	// tradingPath é o caminho (relativo ao locale) da página de comércio,
	// usado tanto pela busca (GET) quanto pelas Server Actions (POST).
	tradingPath = "/intro/shop-search/trading"

	// marketPricePath é o caminho da página de preços de mercado, que resume
	// por quanto cada item andou sendo vendido no servidor.
	marketPricePath = "/intro/shop-search/market-price"

	// buildID aparece na primeira linha de toda resposta Flight. O valor em
	// si é irrelevante para o client; está aqui só para que o formato do
	// payload fique fiel ao real.
	buildID = "mock-build-id"

	// actionChunkPath é o chunk JS que contém a declaração da Server Action.
	// Os demais chunks servidos pela página existem para exercitar a varredura
	// (o descobridor precisa buscar vários até achar o certo).
	actionChunkPath = "/_next/static/chunks/8842-abcdef0123456789.js"
)

// decoyChunkPaths são chunks que a página referencia mas que NÃO contêm a
// declaração da Server Action — o descobridor precisa baixá-los e descartá-los
// antes (ou em paralelo) de achar o certo.
var decoyChunkPaths = []string{
	"/_next/static/chunks/webpack-0000000000000001.js",
	"/_next/static/chunks/main-app-0000000000000002.js",
	"/_next/static/chunks/5525-0000000000000003.js",
}

// Failure é uma resposta de erro injetada no mock, consumida por uma única
// requisição (veja Mock.QueueFailure).
type Failure struct {
	// Status é o código HTTP devolvido.
	Status int
	// RetryAfter, se não vazio, vira o cabeçalho "Retry-After" da resposta.
	RetryAfter string
	// Body é o corpo devolvido; se vazio, o mock usa um texto genérico.
	Body string
	// Malformed faz o mock responder 200 com um payload Flight sintaticamente
	// válido mas sem os campos que o client procura — simula o site
	// devolvendo uma página de erro no lugar do envelope da action. Quando
	// true, Status e RetryAfter são ignorados.
	Malformed bool
	// Passthrough consome uma posição da fila sem falhar nada: serve para
	// mirar em uma chamada específica no meio de uma sequência (por exemplo,
	// deixar o detalhe da loja passar e derrubar só o histórico de preço).
	Passthrough bool
	// Delay segura a resposta por esse tempo antes de devolvê-la. Combinado
	// com Passthrough, simula um upstream lento — o que torna possível
	// observar de forma determinística o estado "em andamento" na interface,
	// em vez de torcer para acertar uma janela de milissegundos.
	Delay time.Duration
}

// RecordedRequest é uma requisição recebida pelo mock, guardada para os
// testes poderem afirmar o que o client de fato enviou.
type RecordedRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   string
}

// Config semeia o mock com as respostas que ele deve devolver.
type Config struct {
	// Locale é o segmento de idioma das rotas. Vazio usa DefaultLocale.
	Locale string
	// ActionID é o hash da Server Action aceito nas chamadas POST e
	// publicado no chunk JS. Vazio usa DefaultActionID.
	ActionID string
	// Searches mapeia o termo buscado (searchWord) para o resultado. Um termo
	// não registrado devolve uma lista vazia, não um erro — é o que o site faz.
	Searches map[string]SearchResult
	// MarketPrices mapeia servidor + termo + período para o resumo de preços
	// praticados. A chave inclui o período porque a mesma busca devolve
	// números diferentes (e listas de tamanhos diferentes) em cada janela —
	// um item vendido há um mês aparece em "ALL" e não aparece em "7".
	MarketPrices map[MarketPriceScope]MarketPriceResult
	// Stores e Items mapeiam o ssi da loja para os detalhes correspondentes.
	Stores map[string]StoreDetail
	Items  map[string]ItemDetail
	// Prices mapeia o itemId para o histórico de preço.
	Prices map[int]PriceHistory
}

// MarketPriceScope identifica uma busca de preços de mercado pelo trio que o
// site de fato recebe na query string: servidor, termo procurado e período.
type MarketPriceScope struct {
	ServerType string
	SearchWord string
	Period     string
}

// Mock é o http.Handler que implementa o site falso. Use New para obter um
// servidor HTTP pronto em testes de Go, ou monte-o diretamente sobre um
// http.Server quando precisar de um processo de verdade (veja
// internal/gnjoytest/cmd/mockgnjoy).
type Mock struct {
	mu           sync.Mutex
	locale       string
	actionID     string
	searches     map[string]SearchResult
	marketPrices map[MarketPriceScope]MarketPriceResult
	stores       map[string]StoreDetail
	items        map[string]ItemDetail
	prices       map[int]PriceHistory
	failures     []Failure
	requests     []RecordedRequest
	unknownID    int
}

// NewMock monta o handler do site falso a partir da configuração informada.
func NewMock(cfg Config) *Mock {
	m := &Mock{
		locale:       cfg.Locale,
		actionID:     cfg.ActionID,
		searches:     cfg.Searches,
		marketPrices: cfg.MarketPrices,
		stores:       cfg.Stores,
		items:        cfg.Items,
		prices:       cfg.Prices,
	}
	if m.locale == "" {
		m.locale = DefaultLocale
	}
	if m.actionID == "" {
		m.actionID = DefaultActionID
	}
	if m.searches == nil {
		m.searches = map[string]SearchResult{}
	}
	if m.marketPrices == nil {
		m.marketPrices = map[MarketPriceScope]MarketPriceResult{}
	}
	if m.stores == nil {
		m.stores = map[string]StoreDetail{}
	}
	if m.items == nil {
		m.items = map[string]ItemDetail{}
	}
	if m.prices == nil {
		m.prices = map[int]PriceHistory{}
	}
	return m
}

// Server é um Mock já servindo em um httptest.Server. A URL base a passar
// para gnjoy.WithBaseURL é o campo URL do httptest.Server embutido.
type Server struct {
	*httptest.Server
	*Mock
}

// New sobe o site falso em um httptest.Server. Cabe a quem chama fechá-lo
// (defer srv.Close()).
func New(cfg Config) *Server {
	mock := NewMock(cfg)
	return &Server{Server: httptest.NewServer(mock), Mock: mock}
}

// QueueFailure enfileira n respostas de erro: as próximas n requisições (de
// qualquer rota) recebem f em vez da resposta normal. Como o rate limiter do
// client serializa tudo, a ordem de consumo é determinística.
func (m *Mock) QueueFailure(f Failure, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for range n {
		m.failures = append(m.failures, f)
	}
}

// SetActionID troca o hash da Server Action aceito pelo mock, simulando um
// novo deploy do site: as chamadas com o hash antigo passam a responder 404 e
// o client precisa redescobrir o valor atual nos chunks.
func (m *Mock) SetActionID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actionID = id
}

// ActionID devolve o hash da Server Action atualmente aceito.
func (m *Mock) ActionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.actionID
}

// SetSearch registra (ou substitui) o resultado devolvido para um termo.
func (m *Mock) SetSearch(searchWord string, result SearchResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searches[searchWord] = result
}

// SetSearches troca o conjunto inteiro de buscas registradas, descartando o
// anterior. Serve para devolver o mock ao estado semeado quando um teste
// injetou um anúncio que os seguintes não devem enxergar.
func (m *Mock) SetSearches(searches map[string]SearchResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if searches == nil {
		searches = map[string]SearchResult{}
	}
	m.searches = searches
}

// SetStore registra (ou substitui) os detalhes da loja de um ssi.
func (m *Mock) SetStore(ssi string, store StoreDetail) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stores[ssi] = store
}

// Requests devolve uma cópia do log de requisições recebidas, na ordem em que
// chegaram.
func (m *Mock) Requests() []RecordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// RequestCount é quantas requisições o mock recebeu até agora.
func (m *Mock) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// ResetRequests limpa o log de requisições, útil para isolar a contagem de
// uma etapa específica do teste.
func (m *Mock) ResetRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = nil
}

// ClearFailures descarta as falhas ainda enfileiradas. Vale a pena chamar
// entre testes que compartilham o mesmo mock: uma falha enfileirada e não
// consumida (porque a chamada custou menos requisições do que o teste
// supôs) sobra para o teste seguinte e o derruba por um motivo que não tem
// nada a ver com ele.
func (m *Mock) ClearFailures() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures = nil
}

func (m *Mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	m.mu.Lock()
	m.requests = append(m.requests, RecordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.Query(),
		Header: r.Header.Clone(),
		Body:   string(body),
	})
	var failure *Failure
	if len(m.failures) > 0 {
		f := m.failures[0]
		m.failures = m.failures[1:]
		failure = &f
	}
	m.mu.Unlock()

	if failure != nil {
		if failure.Delay > 0 {
			select {
			case <-time.After(failure.Delay):
			case <-r.Context().Done():
				return
			}
		}
		if !failure.Passthrough {
			m.writeFailure(w, *failure)
			return
		}
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/"+m.locale+tradingPath:
		m.handleAction(w, r, body)
	case r.Method == http.MethodGet && r.URL.Path == "/"+m.locale+tradingPath:
		// O site devolve Flight para uma navegação do Next.js (rsc: 1) e o
		// HTML da página para um GET comum — que é o que a descoberta do
		// action id usa para achar os chunks.
		if r.Header.Get("rsc") == "1" {
			m.handleSearch(w, r)
			return
		}
		m.writeHTMLPage(w)
	case r.Method == http.MethodGet && r.URL.Path == "/"+m.locale+marketPricePath:
		m.handleMarketPrice(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_next/static/chunks/"):
		m.writeChunk(w, r.URL.Path)
	default:
		http.NotFound(w, r)
	}
}

func (m *Mock) writeFailure(w http.ResponseWriter, f Failure) {
	if f.Malformed {
		// 200 com Flight válido, mas sem "data"/"success" nem
		// "list"/"totalCount": é o que o client vê quando o site devolve uma
		// página de erro no lugar do envelope esperado.
		w.Header().Set("content-type", "text/x-component")
		fmt.Fprintf(w, "0:{\"a\":\"$@1\",\"f\":\"\",\"b\":%q}\n", buildID)
		fmt.Fprint(w, "1:{\"erro\":\"algo inesperado\"}\n")
		return
	}
	if f.RetryAfter != "" {
		w.Header().Set("Retry-After", f.RetryAfter)
	}
	status := f.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	body := f.Body
	if body == "" {
		body = "erro simulado pelo mock"
	}
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

// handleSearch responde a busca no formato Flight, com a lista aninhada dentro
// de um array — como no payload real, onde o objeto com "list"/"totalCount"
// aparece dentro de um elemento de árvore React.
func (m *Mock) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	searchWord := q.Get("searchWord")

	m.mu.Lock()
	result, ok := m.searches[searchWord]
	m.mu.Unlock()
	if !ok {
		result = SearchResult{Items: []ShopListItem{}}
	}
	total := result.TotalCount
	if total == 0 {
		total = len(result.Items)
	}
	items := result.Items
	if items == nil {
		items = []ShopListItem{}
	}

	payload, err := json.Marshal([]any{
		"$", "$L12", nil,
		map[string]any{
			"queryParams": map[string]string{
				"storeType":  q.Get("storeType"),
				"serverType": q.Get("serverType"),
				"searchWord": searchWord,
			},
			"list":       items,
			"totalCount": total,
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("content-type", "text/x-component")
	m.writeFlightNoise(w)
	fmt.Fprintf(w, "10:%s\n", payload)
}

// handleMarketPrice responde a busca de preços de mercado, no mesmo formato
// Flight da busca de lojas — é a mesma página em Next.js, só que outra rota.
func (m *Mock) handleMarketPrice(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := MarketPriceScope{
		ServerType: q.Get("serverType"),
		SearchWord: q.Get("searchWord"),
		Period:     q.Get("period"),
	}

	m.mu.Lock()
	result := m.marketPrices[scope]
	m.mu.Unlock()

	total := result.TotalCount
	if total == 0 {
		total = len(result.Items)
	}
	items := result.Items
	if items == nil {
		items = []MarketPriceItem{}
	}

	payload, err := json.Marshal([]any{
		"$", "$L12", nil,
		map[string]any{
			"queryParams": map[string]string{
				"serverType": scope.ServerType,
				"searchWord": scope.SearchWord,
			},
			"list":       items,
			"totalCount": total,
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("content-type", "text/x-component")
	m.writeFlightNoise(w)
	fmt.Fprintf(w, "10:%s\n", payload)
}

// writeFlightNoise escreve as linhas que acompanham toda resposta Flight e que
// o client tem de ignorar: referências de módulo (que nem são JSON), linhas
// sem o separador ":" e objetos JSON válidos mas sem os campos procurados.
func (m *Mock) writeFlightNoise(w io.Writer) {
	fmt.Fprintf(w, "0:{\"a\":\"$@1\",\"f\":\"\",\"b\":%q}\n", buildID)
	fmt.Fprint(w, "3:I[59665,[],\"OutletBoundary\"]\n")
	fmt.Fprint(w, "linha sem dois pontos\n")
	fmt.Fprint(w, "9:[[\"$\",\"meta\",\"0\",{\"charSet\":\"utf-8\"}]]\n")
}

// handleAction responde às Server Actions de detalhe (loja, item e histórico
// de preço), todas multiplexadas pelo mesmo endpoint POST via o campo "type".
func (m *Mock) handleAction(w http.ResponseWriter, r *http.Request, body []byte) {
	// Um hash desatualizado (site redeployado) faz o Next.js não reconhecer a
	// action e responder 404 — é esse o sinal que dispara a redescoberta.
	if r.Header.Get("next-action") != m.ActionID() {
		http.Error(w, "action não encontrada", http.StatusNotFound)
		return
	}

	var reqs []ActionRequest
	if err := json.Unmarshal(body, &reqs); err != nil || len(reqs) == 0 {
		http.Error(w, "payload inválido", http.StatusBadRequest)
		return
	}
	action := reqs[0]

	ssi, _ := action.Params["ssi"].(string)

	m.mu.Lock()
	defer m.mu.Unlock()

	switch action.Type {
	case "store":
		store, ok := m.stores[ssi]
		if !ok {
			writeFlightAction(w, nil, false)
			return
		}
		writeFlightAction(w, store, true)
	case "item":
		item, ok := m.items[ssi]
		if !ok {
			writeFlightAction(w, nil, false)
			return
		}
		writeFlightAction(w, item, true)
	case "price":
		// O JSON decodifica números como float64; o itemId chega assim.
		itemIdF, _ := action.Params["itemId"].(float64)
		history, ok := m.prices[int(itemIdF)]
		if !ok {
			writeFlightAction(w, PriceHistory{}, true)
			return
		}
		writeFlightAction(w, history, true)
	default:
		writeFlightAction(w, nil, false)
	}
}

// writeFlightAction escreve o envelope de resposta de uma Server Action, no
// mesmo formato de duas linhas que o site usa.
func writeFlightAction(w http.ResponseWriter, data any, success bool) {
	payload, err := json.Marshal(map[string]any{"data": data, "success": success})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "text/x-component")
	fmt.Fprintf(w, "0:{\"a\":\"$@1\",\"f\":\"\",\"b\":%q}\n", buildID)
	fmt.Fprintf(w, "1:%s\n", payload)
}

// writeHTMLPage devolve o HTML da página de comércio, referenciando os chunks
// JS — é por aqui que a descoberta do action id começa.
func (m *Mock) writeHTMLPage(w http.ResponseWriter) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	fmt.Fprint(w, "<!doctype html><html><head>")
	for _, path := range decoyChunkPaths {
		fmt.Fprintf(w, `<link rel="preload" href="%s" as="script"/>`, path)
	}
	fmt.Fprint(w, "</head><body>")
	fmt.Fprintf(w, `<script src="%s" async=""></script>`, actionChunkPath)
	fmt.Fprint(w, "</body></html>")
}

// writeChunk devolve o conteúdo de um chunk JS. Só um deles declara a Server
// Action; os outros são código qualquer, para que a varredura precise de
// verdade procurar entre vários arquivos.
func (m *Mock) writeChunk(w http.ResponseWriter, path string) {
	w.Header().Set("content-type", "application/javascript")
	if path != actionChunkPath {
		fmt.Fprint(w, "(self.webpackChunk=self.webpackChunk||[]).push([[5525],{}]);\n")
		return
	}
	fmt.Fprintf(w,
		"var s=(0,r.createServerReference)(%q,r.callServer,void 0,r.findSourceMapURL,\"getTradingData\");\n",
		m.ActionID())
}
