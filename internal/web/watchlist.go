package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type watchlistPriceView struct {
	Found    bool  `json:"found"`
	MinPrice int64 `json:"minPrice,omitempty"`
	Refine   *int  `json:"refine,omitempty"`
}

// WatchlistPrice trata GET /web/watchlist/price e devolve, em JSON, o menor
// preço atualmente anunciado para um item (identificado por itemId, já que
// a busca por nome pode casar itens diferentes cujo nome contém a mesma
// palavra — ver teste com "Espada") e, quando a loja mais barata for um
// equipamento (arma ou armadura), o refino dessa unidade específica.
//
// A watchlist em si (quais itens, preço alvo, ligado/desligado) é mantida
// inteiramente no navegador (localStorage, ver internal/web/static/watchlist.js)
// — este endpoint só fornece o dado de mercado ao vivo que o navegador não
// tem como buscar sozinho (rate limiting, action id da Server Action etc.
// são responsabilidade do gnjoy.Client, não do JS do cliente).
func (h *Handler) WatchlistPrice(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := strings.TrimSpace(q.Get("server"))
	item := strings.TrimSpace(q.Get("item"))
	itemId, err := strconv.Atoi(strings.TrimSpace(q.Get("itemId")))

	if server == "" || item == "" || err != nil {
		http.Error(w, "os parâmetros 'server', 'item' e 'itemId' são obrigatórios", http.StatusBadRequest)
		return
	}

	result, err := h.client.SearchShops(r.Context(), gnjoy.SearchShopsParams{
		ServerType: server,
		StoreType:  gnjoy.StoreTypeBuy,
		SearchWord: item,
	})
	if err != nil {
		slog.Error("web: watchlist: busca de preço falhou", "error", err)
		http.Error(w, "não foi possível consultar o mercado agora", http.StatusBadGateway)
		return
	}

	var cheapest *gnjoy.ShopListItem
	for i := range result.Items {
		it := &result.Items[i]
		if it.ItemId != itemId {
			continue
		}
		if cheapest == nil || it.ItemPrice < cheapest.ItemPrice {
			cheapest = it
		}
	}
	if cheapest == nil {
		writeJSON(w, http.StatusOK, watchlistPriceView{Found: false})
		return
	}

	view := watchlistPriceView{Found: true, MinPrice: cheapest.ItemPrice}
	if cheapest.DatabaseType == "weapon" || cheapest.DatabaseType == "armor" {
		loc := gnjoy.StoreLocation{SvrId: cheapest.SvrId, MapId: cheapest.MapId, SSI: cheapest.SSI}
		if detail, err := h.client.GetStoreDetail(r.Context(), loc); err == nil {
			view.Refine = &detail.Refine
		} else {
			slog.Warn("web: watchlist: não foi possível obter refino da loja mais barata", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("web: falha ao codificar resposta JSON", "error", err)
	}
}
