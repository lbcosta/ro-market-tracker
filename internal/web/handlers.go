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
	Error    string
	Server   string
	ItemID   int
	ItemName string
	Items    []gnjoy.ShopListItem

	SortBy       string
	SortDir      string
	PriceSortURL string
	QtySortURL   string
	PriceArrow   string
	QtyArrow     string
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

	view := resultsView{
		Server:  server,
		Items:   result.Items,
		SortBy:  sortBy,
		SortDir: sortDir,
	}
	if len(result.Items) > 0 {
		// O nome digitado pelo usuário é só a palavra de busca; o nome
		// canônico do item (acentuação, capitalização corretas) só é
		// conhecido a partir do que a busca de fato encontrou. Isso é lido
		// antes de ordenar, na ordem em que a API devolveu, para não trocar
		// de item conforme a coluna/direção de ordenação escolhida.
		view.ItemName = result.Items[0].ItemName
		view.ItemID = result.Items[0].ItemId
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
