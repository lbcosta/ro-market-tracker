package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type watchlistPriceView struct {
	Found       bool   `json:"found"`
	MinPrice    int64  `json:"minPrice,omitempty"`
	Refine      *int   `json:"refine,omitempty"`
	NaviCommand string `json:"naviCommand,omitempty"`
}

// isEquipment diz se o item pode ter refino. O site usa um tipo só ("armor")
// para tudo que se veste — armaduras, acessórios e chapéus —, então esta
// checagem cobre os três; consumíveis, cartas e materiais nunca têm refino, e
// consultar o detalhe deles seria uma requisição jogada fora.
func isEquipment(databaseType string) bool {
	return databaseType == "weapon" || databaseType == "armor"
}

// locationKey identifica um anúncio nos memos de detalhe (storeDetailMemo,
// itemDetailMemo) por (svrId, mapId, ssi) — o "endereço" completo da loja, o
// mesmo trio que as rotas de detalhe usam.
func locationKey(loc gnjoy.StoreLocation) string {
	return cacheKey(strconv.Itoa(loc.SvrId), strconv.Itoa(loc.MapId), loc.SSI)
}

// storeLocationOf monta o StoreLocation (o "endereço" svrId+mapId+ssi) de um
// ShopListItem já em mãos — o formato em que a busca e a watchlist carregam
// um anúncio.
func storeLocationOf(item gnjoy.ShopListItem) gnjoy.StoreLocation {
	return gnjoy.StoreLocation{SvrId: item.SvrId, MapId: item.MapId, SSI: item.SSI}
}

// storeKey é locationKey a partir de um ShopListItem já em mãos, sem precisar
// montar um StoreLocation à parte antes de chamar locationKey.
func storeKey(item gnjoy.ShopListItem) string {
	return locationKey(storeLocationOf(item))
}

// WatchlistPrice trata GET /web/watchlist/price e devolve, em JSON, o preço
// atualmente anunciado para um item (identificado por itemId, já que a
// busca por nome pode casar itens diferentes cujo nome contém a mesma
// palavra — ver teste com "Espada").
//
// Sem o parâmetro opcional "refine", devolve o menor preço entre todas as
// lojas com esse itemId, o comando "/navi ..." até a loja mais barata (ver
// naviCommand em navi.go) e, se ela for um equipamento, o refino dela — só
// informativo, "o que está mais barato agora, onde fica e qual o refino
// dessa unidade em especial".
//
// Com "refine" informado, a busca passa a ser por uma unidade NESSE refino
// específico: entre as lojas com esse itemId, ordenadas por preço crescente,
// procura a mais barata cujo refino bate exatamente com o pedido. Como a
// única forma de saber o refino de uma loja é consultando o detalhe dela (a
// busca por nome não traz essa informação), cada refino descoberto é
// memoizado por loja (ver Handler.refineMemo) e uma checagem consulta no
// máximo maxRefineDetailFetches lojas novas — ver watchlistPriceForRefine.
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
// descoberto fica no refineMemo, então o orçamento vale só para lojas novas: a
// cada ciclo a cobertura cresce em até este tanto de lojas, sem nunca custar
// mais que isso — um item popular com dezenas de anúncios converge em poucos
// ciclos em vez de custar dezenas de chamadas em cada um.
const maxRefineDetailFetches = 8

// fetchStoreDetail consulta o detalhe de uma loja — do memo compartilhado ou
// do upstream (ver Handler.storeDetail). NoRetry: isto roda dentro de uma
// checagem da watchlist, que tem repetição própria — se o site está pedindo
// calma, desistir é o certo.
//
// O erro sai junto do valor porque a varredura da busca (ver bonus.go) precisa
// distinguir "consultei e não deu certo" de "o site parou de responder": lá,
// ao contrário daqui, o primeiro erro aborta o resto da varredura.
func (h *Handler) fetchStoreDetail(ctx context.Context, item gnjoy.ShopListItem) (*gnjoy.StoreDetail, error) {
	detail, err := h.storeDetail(ctx, storeLocationOf(item), gnjoy.NoRetry())
	if err != nil {
		// Debug, e não Warn: uma varredura de busca com o site instável
		// geraria uma linha destas por anúncio.
		slog.Debug("web: não foi possível obter o detalhe de um anúncio", "error", err)
		return nil, err
	}
	return detail, nil
}

// lookupDetail devolve o detalhe completo de uma loja — refino e localização
// —, do memo ou do upstream, consumindo, neste segundo caso, uma unidade do
// orçamento da checagem.
//
// affordable falso significa "não estava no memo e o orçamento acabou": o
// candidato não foi descartado, só ficou para um próximo ciclo. detail nil
// (com affordable true) significa que a consulta foi feita e não deu certo.
func (h *Handler) lookupDetail(r *http.Request, item gnjoy.ShopListItem, budget *int) (detail *gnjoy.StoreDetail, affordable bool) {
	if cached, ok := h.storeDetailMemo.peek(storeKey(item)); ok {
		return cached, true
	}
	if *budget == 0 {
		return nil, false
	}
	*budget--
	detail, err := h.fetchStoreDetail(r.Context(), item)
	if err != nil {
		return nil, true
	}
	return detail, true
}

// watchlistPriceForCheapest também busca o detalhe da loja mais barata —
// mesmo quando o item não é equipamento e por isso não precisa de refino —
// porque é dali que sai a localização (NaviCommand): o card só mostra o
// botão de copiar quando o front-end decide que vale a pena (alvo atingido
// ou item disponível), mas o servidor não sabe qual é o alvo do usuário (ele
// vive só no navegador — ver watchlist.js), então busca sempre que encontra
// um anúncio. O custo real fica baixo na prática: a mesma loja tende a
// continuar sendo a mais barata de uma checagem para a próxima, e a segunda
// em diante é só um peek no memo (ver lookupDetail).
func (h *Handler) watchlistPriceForCheapest(r *http.Request, candidates []gnjoy.ShopListItem) watchlistPriceView {
	if len(candidates) == 0 {
		return watchlistPriceView{Found: false}
	}
	cheapest := candidates[0]
	view := watchlistPriceView{Found: true, MinPrice: cheapest.ItemPrice}

	budget := 1
	if detail, _ := h.lookupDetail(r, cheapest, &budget); detail != nil {
		view.NaviCommand = naviCommand(detail.MapName, detail.Xpos, detail.Ypos)
		if isEquipment(cheapest.DatabaseType) {
			refine := detail.Refine
			view.Refine = &refine
		}
	}
	return view
}

// watchlistPriceForRefine procura o anúncio mais barato no refino que a
// entrada da watchlist fixou.
//
// A varredura é por preço crescente e para no primeiro candidato que serve —
// os mais caros nem chegam a ser consultados. O orçamento
// (maxRefineDetailFetches) limita quantas lojas AINDA DESCONHECIDAS uma
// checagem consulta; as demais ficam para os próximos ciclos, que encontram no
// memo o que este descobriu.
func (h *Handler) watchlistPriceForRefine(r *http.Request, candidates []gnjoy.ShopListItem, wantRefine int) watchlistPriceView {
	budget := maxRefineDetailFetches
	for _, candidate := range candidates {
		detail, affordable := h.lookupDetail(r, candidate, &budget)
		if !affordable {
			// Orçamento do ciclo esgotado: esta loja fica para o próximo (o
			// memo guarda as já descobertas, então lá o orçamento avança para
			// lojas realmente novas). A varredura continua: um candidato mais
			// caro pode já estar memoizado de um ciclo anterior e responder a
			// consulta agora.
			continue
		}
		if detail != nil && detail.Refine == wantRefine {
			refine := detail.Refine
			return watchlistPriceView{
				Found:       true,
				MinPrice:    candidate.ItemPrice,
				Refine:      &refine,
				NaviCommand: naviCommand(detail.MapName, detail.Xpos, detail.Ypos),
			}
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
