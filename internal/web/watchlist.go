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
// pedido. Como a única forma de saber o refino de uma loja é consultando o
// detalhe dela (a busca por nome não traz essa informação), cada refino
// descoberto é memoizado por ssi (ver Handler.refineMemo) e uma checagem
// consulta no máximo maxRefineDetailFetches lojas novas — ver
// watchlistPriceForRefine.
//
// A busca em si passa pelo cache de resultados (cachedSearchShops): a
// checagem automática aceita um resultado de até monitorMaxAge — é o que
// evita que várias abas, cada uma com seu próprio ciclo, multipliquem o
// tráfego ao upstream. Com "fresh=1" (o botão "↻" de atualizar agora), o
// cache é ignorado e a busca vai ao upstream de verdade.
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

	maxAge := monitorMaxAge
	if q.Get("fresh") == "1" {
		// O usuário pediu dados novos de propósito: nada de cache. O rate
		// limiter do client continua valendo, então nem segurar o botão
		// apertado vira rajada contra o site.
		maxAge = 0
	}

	// NoRetry: se o site estiver pedindo calma, esta checagem desiste — o
	// próximo ciclo da watchlist (5 min) refaz a consulta de qualquer forma.
	result, err := h.cachedSearchShops(r.Context(), server, item, maxAge, gnjoy.NoRetry())
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

// maxRefineDetailFetches é o orçamento de consultas de detalhe de loja que
// UMA checagem com refino fixado pode gastar no upstream. O que já foi
// descoberto fica no refineMemo, então o orçamento vale só para lojas
// novas: a cada ciclo a cobertura cresce em até este tanto de lojas, sem
// nunca custar mais que isso — um item popular com dezenas de anúncios
// converge em poucos ciclos em vez de custar dezenas de chamadas em cada um.
const maxRefineDetailFetches = 8

// refineMemoKey identifica uma loja no refineMemo — o "endereço" completo
// dela, o mesmo trio que GetStoreDetail usa.
func refineMemoKey(item gnjoy.ShopListItem) string {
	return cacheKey(strconv.Itoa(item.SvrId), strconv.Itoa(item.MapId), item.SSI)
}

// fetchStoreRefine consulta o refino de uma loja no upstream e o memoiza.
// NoRetry: isto roda dentro de uma checagem da watchlist, que tem repetição
// própria — se o site está pedindo calma, desistir é o certo.
func (h *Handler) fetchStoreRefine(r *http.Request, item gnjoy.ShopListItem) (int, bool) {
	loc := gnjoy.StoreLocation{SvrId: item.SvrId, MapId: item.MapId, SSI: item.SSI}
	detail, err := h.client.GetStoreDetail(r.Context(), loc, gnjoy.NoRetry())
	if err != nil {
		slog.Warn("web: watchlist: não foi possível obter o refino de um anúncio", "error", err)
		return 0, false
	}
	h.refineMemo.put(refineMemoKey(item), detail.Refine)
	return detail.Refine, true
}

func (h *Handler) watchlistPriceForCheapest(r *http.Request, candidates []gnjoy.ShopListItem) watchlistPriceView {
	if len(candidates) == 0 {
		return watchlistPriceView{Found: false}
	}
	cheapest := candidates[0]
	view := watchlistPriceView{Found: true, MinPrice: cheapest.ItemPrice}
	if cheapest.DatabaseType == "weapon" || cheapest.DatabaseType == "armor" {
		refine, ok := h.refineMemo.peek(refineMemoKey(cheapest))
		if !ok {
			refine, ok = h.fetchStoreRefine(r, cheapest)
		}
		if ok {
			view.Refine = &refine
		}
	}
	return view
}

func (h *Handler) watchlistPriceForRefine(r *http.Request, candidates []gnjoy.ShopListItem, wantRefine int) watchlistPriceView {
	budget := maxRefineDetailFetches
	for _, candidate := range candidates {
		refine, known := h.refineMemo.peek(refineMemoKey(candidate))
		if !known {
			if budget == 0 {
				// Orçamento do ciclo esgotado: esta loja fica para o próximo
				// ciclo (o memo guarda as já descobertas, então lá o orçamento
				// avança para lojas realmente novas). A varredura continua:
				// um candidato mais caro pode já estar memoizado de um ciclo
				// anterior e responder a consulta agora.
				continue
			}
			budget--
			refine, known = h.fetchStoreRefine(r, candidate)
			if !known {
				continue
			}
		}
		if refine == wantRefine {
			return watchlistPriceView{Found: true, MinPrice: candidate.ItemPrice, Refine: &refine}
		}
	}
	// Não achou dentro do que o memo e o orçamento deste ciclo alcançaram.
	// Pode existir uma unidade nesse refino entre as lojas ainda não
	// consultadas — os próximos ciclos ampliam a cobertura até lá.
	return watchlistPriceView{Found: false}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("web: falha ao codificar resposta JSON", "error", err)
	}
}
