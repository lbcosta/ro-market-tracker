package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type Handler struct {
	client *gnjoy.Client
}

func NewHandler(client *gnjoy.Client) *Handler {
	return &Handler{client: client}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	render(w, "index.html.tmpl", nil)
}

type resultsView struct {
	Error    string
	ItemName string
	Items    []gnjoy.ShopListItem
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
	view := resultsView{Items: result.Items}
	if len(result.Items) > 0 {
		// O nome digitado pelo usuário é só a palavra de busca; o nome
		// canônico do item (acentuação, capitalização corretas) só é
		// conhecido a partir do que a busca de fato encontrou.
		view.ItemName = result.Items[0].ItemName
	}
	render(w, "results.html.tmpl", view)
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
