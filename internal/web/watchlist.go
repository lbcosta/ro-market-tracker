package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type watchlistPriceView struct {
	Found    bool  `json:"found"`
	MinPrice int64 `json:"minPrice,omitempty"`
	Refine   *int  `json:"refine,omitempty"`
}

// WatchlistPrice trata GET /web/watchlist/price e devolve, em JSON, o preço
// atualmente anunciado para um item (identificado por itemId, já que a
// busca por nome pode casar itens diferentes cujo nome contém a mesma
// palavra — ver teste com "Espada").
//
// Sem o parâmetro opcional "refine", devolve o menor preço entre todas as
// lojas com esse itemId e, se a mais barata for um equipamento (arma ou
// armadura), o refino dela — só informativo, "o que está mais barato agora
// e qual o refino dessa unidade em especial".
//
// Com "refine" informado, a busca passa a ser por uma unidade NESSE refino
// específico: entre as lojas com esse itemId, ordenadas por preço
// crescente, procura a mais barata cujo refino bate exatamente com o
// pedido — consultando o detalhe de cada loja candidata, uma de cada vez,
// até achar (ou esgotar as opções). Como a única forma de saber o refino de
// uma loja é consultando o detalhe dela (a busca por nome não traz essa
// informação), isso pode custar várias chamadas ao upstream para um item
// com muitos anúncios; o rate limiter do client (gnjoy.Client) já
// serializa tudo, então o efeito é essa checagem demorar mais, não rajadas
// de requisições.
//
// A watchlist em si (quais itens, preço alvo, refino travado,
// ligado/desligado) é mantida inteiramente no navegador (localStorage, ver
// internal/web/static/watchlist.js) — este endpoint só fornece o dado de
// mercado ao vivo que o navegador não tem como buscar sozinho.
func (h *Handler) WatchlistPrice(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := strings.TrimSpace(q.Get("server"))
	item := strings.TrimSpace(q.Get("item"))
	itemId, err := strconv.Atoi(strings.TrimSpace(q.Get("itemId")))
	if server == "" || item == "" || err != nil {
		http.Error(w, "os parâmetros 'server', 'item' e 'itemId' são obrigatórios", http.StatusBadRequest)
		return
	}

	var refineFilter *int
	if raw := strings.TrimSpace(q.Get("refine")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			http.Error(w, "'refine' deve ser um número inteiro não negativo", http.StatusBadRequest)
			return
		}
		refineFilter = &v
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

	var candidates []gnjoy.ShopListItem
	for _, it := range result.Items {
		if it.ItemId == itemId {
			candidates = append(candidates, it)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ItemPrice < candidates[j].ItemPrice })

	var view watchlistPriceView
	if refineFilter == nil {
		view = h.watchlistPriceForCheapest(r, candidates)
	} else {
		view = h.watchlistPriceForRefine(r, candidates, *refineFilter)
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) watchlistPriceForCheapest(r *http.Request, candidates []gnjoy.ShopListItem) watchlistPriceView {
	if len(candidates) == 0 {
		return watchlistPriceView{Found: false}
	}
	cheapest := candidates[0]
	view := watchlistPriceView{Found: true, MinPrice: cheapest.ItemPrice}
	if cheapest.DatabaseType == "weapon" || cheapest.DatabaseType == "armor" {
		loc := gnjoy.StoreLocation{SvrId: cheapest.SvrId, MapId: cheapest.MapId, SSI: cheapest.SSI}
		if detail, err := h.client.GetStoreDetail(r.Context(), loc); err == nil {
			view.Refine = &detail.Refine
		} else {
			slog.Warn("web: watchlist: não foi possível obter refino da loja mais barata", "error", err)
		}
	}
	return view
}

func (h *Handler) watchlistPriceForRefine(r *http.Request, candidates []gnjoy.ShopListItem, wantRefine int) watchlistPriceView {
	for _, candidate := range candidates {
		loc := gnjoy.StoreLocation{SvrId: candidate.SvrId, MapId: candidate.MapId, SSI: candidate.SSI}
		detail, err := h.client.GetStoreDetail(r.Context(), loc)
		if err != nil {
			slog.Warn("web: watchlist: não foi possível obter refino de um candidato", "error", err)
			continue
		}
		if detail.Refine == wantRefine {
			refine := detail.Refine
			return watchlistPriceView{Found: true, MinPrice: candidate.ItemPrice, Refine: &refine}
		}
	}
	return watchlistPriceView{Found: false}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("web: falha ao codificar resposta JSON", "error", err)
	}
}
