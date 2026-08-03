package web

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type Handler struct {
	client *gnjoy.Client

	// warmupOnce garante que o aquecimento do action id (ver warmupActionID)
	// só dispare uma vez por processo, mesmo que a página seja recarregada
	// várias vezes.
	warmupOnce sync.Once
}

func NewHandler(client *gnjoy.Client) *Handler {
	return &Handler{client: client}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	h.warmupActionID()
	render(w, "index.html.tmpl", nil)
}

// warmupActionID testa o action id da Server Action do Next.js em segundo
// plano assim que a página é aberta pela primeira vez, em vez de esperar a
// primeira ação do usuário (expandir uma linha) topar com o id desatualizado
// e pagar o custo da redescoberta no meio da interação — o que dá a
// impressão de o site estar lento. O teste (gnjoy.Client.WarmupActionID)
// gasta só uma requisição mínima no caso comum (id já válido); a
// redescoberta completa, mais cara, só é disparada se de fato for
// necessária. Roda desacoplada do contexto da requisição (que seria
// cancelado assim que Index devolvesse a resposta) e só uma vez por
// processo: qualquer chamada de action que mais tarde encontrar o id
// realmente desatualizado (ex.: um novo deploy do GnJoy) continua se
// autocorrigindo sozinha (ver gnjoy.Client.callAction).
func (h *Handler) warmupActionID() {
	h.warmupOnce.Do(func() {
		go func() {
			if err := h.client.WarmupActionID(context.Background()); err != nil {
				slog.Debug("web: aquecimento do action id não confirmou a chave, seguirá tentando sob demanda", "error", err)
			}
		}()
	})
}

type resultsView struct {
	Error  string
	Server string
	Query  string
	Items  []gnjoy.ShopListItem
	Groups []resultsGroup

	SortBy       string
	SortDir      string
	PriceSortURL string
	QtySortURL   string
	PriceArrow   string
	QtyArrow     string
}

// resultsGroup é a seção da tabela de resultados com todos os anúncios de um
// mesmo item de catálogo (mesmo ItemID). Uma busca por palavra pode casar
// vários itens de nomes diferentes (ex.: "Espada" acha "Espada Primordial",
// "Espada Citadina" e "Carta Peixe-Espada"); sem agrupar, um único botão "+
// Watchlist" no topo da tabela não tem como dizer qual desses itens ele
// adiciona.
type resultsGroup struct {
	ItemID   int
	ItemName string
	Items    []gnjoy.ShopListItem
}

// groupItems agrupa items — já ordenados pela coluna/direção escolhida — por
// ItemID, preservando a ordem de primeira aparição de cada um. Como items já
// chega ordenado e uma subsequência de uma sequência ordenada continua
// ordenada, as linhas dentro de cada grupo saem na mesma ordem relativa que
// teriam na tabela sem agrupamento.
func groupItems(items []gnjoy.ShopListItem) []resultsGroup {
	groups := make([]resultsGroup, 0, len(items))
	index := make(map[int]int, len(items))
	for _, it := range items {
		i, ok := index[it.ItemId]
		if !ok {
			i = len(groups)
			index[it.ItemId] = i
			groups = append(groups, resultsGroup{ItemID: it.ItemId, ItemName: it.ItemName})
		}
		groups[i].Items = append(groups[i].Items, it)
	}
	return groups
}

// sortableColumns mapeia os valores aceitos no parâmetro "sort" para a
// função que extrai a chave de comparação de cada item.
var sortableColumns = map[string]func(gnjoy.ShopListItem) int64{
	"price": func(i gnjoy.ShopListItem) int64 { return i.ItemPrice },
	"qty":   func(i gnjoy.ShopListItem) int64 { return int64(i.ItemCnt) },
}

// Search trata GET /web/search e devolve o fragmento HTML da tabela de
// resultados (ou uma mensagem de erro/vazio), para o HTMX trocar dentro de
// #results.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := strings.TrimSpace(q.Get("server"))
	item := strings.TrimSpace(q.Get("item"))

	if server == "" || item == "" {
		render(w, "results.html.tmpl", resultsView{Error: "Informe o servidor e o nome do item."})
		return
	}

	sortBy := q.Get("sort")
	if _, ok := sortableColumns[sortBy]; !ok {
		// A busca inicial (sem clicar em nenhum cabeçalho) já vem ordenada
		// por preço crescente: é o que torna óbvio, de cara, que a tabela é
		// ordenável — sem isso a seta só aparece depois do primeiro clique.
		sortBy = "price"
	}
	sortDir := q.Get("dir")
	if sortDir != "asc" && sortDir != "desc" {
		sortDir = "asc"
	}

	// O frontend só procura lojas comprando o item (ou seja, anúncios de
	// jogadores vendendo o item, que é o que interessa para quem está
	// rastreando preços) — não há seletor de tipo de negociação na UI.
	result, err := h.client.SearchShops(r.Context(), gnjoy.SearchShopsParams{
		ServerType: server,
		StoreType:  gnjoy.StoreTypeBuy,
		SearchWord: item,
	})
	if err != nil {
		slog.Error("web: busca falhou", "error", err)
		render(w, "results.html.tmpl", resultsView{Error: "Não foi possível buscar no mercado agora. Tente novamente em instantes."})
		return
	}

	if len(result.Items) == 0 {
		// Item fora do mercado atual: em vez de parar em "nenhum resultado",
		// vale checar se ele já foi vendido no servidor alguma vez — é o que
		// interessa a quem quer rastrear um item que ninguém está anunciando
		// no momento (ver history.go).
		render(w, "history.html.tmpl", h.priceHistory(r.Context(), server, item))
		return
	}

	// O título mostra o termo digitado, não o nome canônico de um dos itens
	// casados: a busca por palavra pode casar itens de nomes diferentes (ver
	// resultsGroup), e não há "o" item cujo nome sirva de título — cada
	// grupo já mostra o seu próprio nome canônico no cabeçalho da seção.
	view := resultsView{
		Server:  server,
		Items:   result.Items,
		Query:   item,
		SortBy:  sortBy,
		SortDir: sortDir,
	}

	if key, ok := sortableColumns[sortBy]; ok {
		sort.SliceStable(result.Items, func(i, j int) bool {
			a, b := key(result.Items[i]), key(result.Items[j])
			if sortDir == "desc" {
				return a > b
			}
			return a < b
		})
	}
	view.Groups = groupItems(result.Items)
	view.PriceSortURL = sortURL(server, item, "price", sortBy, sortDir)
	view.QtySortURL = sortURL(server, item, "qty", sortBy, sortDir)
	view.PriceArrow = sortArrow(sortBy, sortDir, "price")
	view.QtyArrow = sortArrow(sortBy, sortDir, "qty")
	render(w, "results.html.tmpl", view)
}

// sortURL monta a URL de /web/search que reordena os resultados por column,
// preservando servidor e termo buscado. Clicar de novo na mesma coluna já
// ordenada inverte a direção; clicar em outra coluna começa em ordem
// ascendente.
func sortURL(server, item, column, currentSortBy, currentDir string) string {
	dir := "asc"
	if currentSortBy == column && currentDir == "asc" {
		dir = "desc"
	}
	v := url.Values{}
	v.Set("server", server)
	v.Set("item", item)
	v.Set("sort", column)
	v.Set("dir", dir)
	return "/web/search?" + v.Encode()
}

// sortArrow devolve o indicador visual de direção para o cabeçalho de
// column, ou vazio se a coluna não for a que está ordenando no momento.
func sortArrow(sortBy, dir, column string) string {
	if sortBy != column {
		return ""
	}
	if dir == "desc" {
		return " ▼"
	}
	return " ▲"
}

type expandView struct {
	Error       string
	Store       *gnjoy.StoreDetail
	Item        *gnjoy.ItemDetail
	NaviCommand string
	Stats       sevenDayStats
}

// Expand trata GET /web/shops/{svrId}/{mapId}/{ssi}/expand: busca o detalhe
// da loja, o detalhe do item e os últimos 7 dias de histórico de preço, e
// devolve o card HTML que o HTMX encaixa na linha de detalhe abaixo do item
// clicado.
func (h *Handler) Expand(w http.ResponseWriter, r *http.Request) {
	loc, ok := parseLocation(w, r)
	if !ok {
		return
	}

	store, err := h.client.GetStoreDetail(r.Context(), loc)
	if err != nil {
		slog.Error("web: detalhe da loja falhou", "error", err)
		render(w, "expand.html.tmpl", expandView{Error: "Não foi possível carregar os detalhes dessa loja agora."})
		return
	}

	item, err := h.client.GetItemDetail(r.Context(), loc, "")
	if err != nil {
		slog.Error("web: detalhe do item falhou", "error", err)
		render(w, "expand.html.tmpl", expandView{Error: "Não foi possível carregar os detalhes desse item agora."})
		return
	}

	history, err := h.client.GetPriceHistory(r.Context(), gnjoy.PriceHistoryParams{
		ItemId: store.ItemId,
		SvrId:  loc.SvrId,
		Page:   1,
		Limit:  7,
	})
	if err != nil {
		slog.Error("web: histórico de preço falhou", "error", err)
		render(w, "expand.html.tmpl", expandView{Error: "Não foi possível carregar o histórico de preços agora."})
		return
	}

	render(w, "expand.html.tmpl", expandView{
		Store:       store,
		Item:        item,
		NaviCommand: naviCommand(store.MapName, store.Xpos, store.Ypos),
		Stats:       computeSevenDayStats(history.DayStatsList),
	})
}

func parseLocation(w http.ResponseWriter, r *http.Request) (gnjoy.StoreLocation, bool) {
	svrId, err := strconv.Atoi(r.PathValue("svrId"))
	if err != nil {
		http.Error(w, "svrId inválido", http.StatusBadRequest)
		return gnjoy.StoreLocation{}, false
	}
	mapId, err := strconv.Atoi(r.PathValue("mapId"))
	if err != nil {
		http.Error(w, "mapId inválido", http.StatusBadRequest)
		return gnjoy.StoreLocation{}, false
	}
	ssi := r.PathValue("ssi")
	if ssi == "" {
		http.Error(w, "ssi inválido", http.StatusBadRequest)
		return gnjoy.StoreLocation{}, false
	}
	return gnjoy.StoreLocation{SvrId: svrId, MapId: mapId, SSI: ssi}, true
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("content-type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("web: falha ao renderizar template", "template", name, "error", err)
	}
}
