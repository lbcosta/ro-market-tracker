package web

import (
	"context"
	"encoding/json"
	"fmt"
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

	// Partial diz que a checagem não chegou a olhar todos os anúncios: o
	// orçamento de consultas acabou antes (ver maxDetailFetches). Sem ele,
	// "Found: false" significaria duas coisas bem diferentes — "ninguém
	// anuncia isso" e "ainda não cheguei lá" —, que é a diferença entre
	// desistir e esperar o próximo ciclo. Com dois filtros ao mesmo tempo a
	// cobertura por checagem cai pela metade, então isso deixa de ser raro.
	Partial bool `json:"partial,omitempty"`

	// Equipment é o que decide se a linha da watchlist oferece os campos de
	// bônus. É PONTEIRO porque "não sei" não é "não é": quando o item não
	// tem anúncio nenhum agora, não há de onde tirar o tipo — e responder
	// false ali faria a linha perder os campos de bônus, junto com os
	// filtros que o usuário digitou neles.
	Equipment *bool `json:"equipment,omitempty"`
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
// Com "refine" e/ou "bonus" informados, a busca passa a ser por uma unidade
// que satisfaça isso: entre as lojas com esse itemId, ordenadas por preço
// crescente, procura a mais barata que bate com o pedido. Nem o refino nem os
// bônus vêm na busca por nome — cada um só é conhecido consultando um detalhe
// (o da loja e o do item, respectivamente), então ambos são memoizados por
// anúncio e uma checagem gasta no máximo maxDetailFetches consultas novas —
// ver watchlistPriceForFilter.
//
// "bonus" é repetido, uma vez por frase exigida (ver parseBonusFilters), e
// não deve ser confundido com o "bonus=1" de /web/search, que é o checkbox da
// varredura.
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

	var filter watchlistFilter
	if raw := strings.TrimSpace(q.Get("refine")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			http.Error(w, "'refine' deve ser um número inteiro não negativo", http.StatusBadRequest)
			return
		}
		filter.Refine = &v
	}
	// Atenção ao homônimo: em /web/search, "bonus" é o checkbox da varredura
	// ("bonus=1"). Aqui é outra coisa — cada ocorrência do parâmetro é UMA
	// frase de bônus a exigir do anúncio.
	filter.Bonus, err = parseBonusFilters(q["bonus"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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
	if filter.empty() {
		view = h.watchlistPriceForCheapest(r, candidates)
	} else {
		view = h.watchlistPriceForFilter(r, candidates, filter)
	}
	// Fora dos dois caminhos acima porque não depende de achar nada: mesmo
	// quando o filtro não casa com anúncio nenhum, o tipo do item continua
	// valendo, e é ele que mantém os campos de bônus na tela. Sai da busca,
	// que já traz o databaseType de todo anúncio — custo zero.
	if len(candidates) > 0 {
		equipamento := isEquipment(candidates[0].DatabaseType)
		view.Equipment = &equipamento
	}
	writeJSON(w, http.StatusOK, view)
}

// watchlistFilter é o que a entrada da watchlist fixou como "o anúncio que me
// interessa". Os dois campos são opcionais e independentes: nenhum deles
// significa "o mais barato, seja qual for"; um ou os dois, "o mais barato
// entre os que satisfizerem isto".
type watchlistFilter struct {
	Refine *int
	Bonus  []string
}

func (f watchlistFilter) empty() bool {
	return f.Refine == nil && len(f.Bonus) == 0
}

// maxBonusFilters acompanha o contrato do site, que expõe quatro bônus por
// anúncio (randomOpt1..4): exigir mais que isso é um filtro impossível por
// construção, e mais provável de ser lixo na URL do que pedido de verdade.
const maxBonusFilters = 4

// maxBonusFilterLen é um teto de sanidade por frase. Os bônus reais são
// curtos ("Conjuração variável -5%"); o que passa disso não veio da tela.
const maxBonusFilterLen = 120

// parseBonusFilters lê as ocorrências do parâmetro "bonus" — uma por frase.
//
// Um parâmetro repetido, e não uma lista com separador: as frases vêm do site
// com pontuação livre ("Dano mágico todas as prop. 5%.", "CRIT +4"), e
// qualquer separador escolhido a dedo teria chance de aparecer dentro de uma
// delas. Repetir o parâmetro deixa a própria codificação da URL resolver.
//
// Frases vazias são descartadas em vez de recusadas: é assim que o front-end
// desfixa um campo de bônus, do mesmo jeito que manda "refine=" vazio ao
// desfixar o refino.
func parseBonusFilters(raw []string) ([]string, error) {
	var filtros []string
	for _, b := range raw {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if len(b) > maxBonusFilterLen {
			return nil, fmt.Errorf("cada 'bonus' deve ter no máximo %d caracteres", maxBonusFilterLen)
		}
		filtros = append(filtros, b)
	}
	if len(filtros) > maxBonusFilters {
		return nil, fmt.Errorf("no máximo %d bônus podem ser exigidos de uma vez", maxBonusFilters)
	}
	return filtros, nil
}

// maxDetailFetches é o orçamento de consultas de detalhe que UMA checagem com
// filtro pode gastar no upstream. O que já foi descoberto fica nos memos,
// então o orçamento vale só para anúncios novos: a cada ciclo a cobertura
// cresce em até este tanto, sem nunca custar mais que isso — um item popular
// com dezenas de anúncios converge em poucos ciclos em vez de custar dezenas
// de chamadas em cada um.
//
// Conta REQUISIÇÕES, não anúncios: refino e bônus vêm de rotas diferentes
// (detalhe da loja e detalhe do item), então um anúncio com os dois filtros
// custa duas. É o teto de tráfego que interessa segurar, não o de candidatos.
const maxDetailFetches = 8

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

// lookupStoreDetail devolve o detalhe completo de uma loja — refino e
// localização —, do memo ou do upstream, consumindo, neste segundo caso, uma
// unidade do orçamento da checagem.
//
// affordable falso significa "não estava no memo e o orçamento acabou": o
// candidato não foi descartado, só ficou para um próximo ciclo. detail nil
// (com affordable true) significa que a consulta foi feita e não deu certo.
func (h *Handler) lookupStoreDetail(r *http.Request, item gnjoy.ShopListItem, budget *int) (detail *gnjoy.StoreDetail, affordable bool) {
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

// lookupItemBonus é o irmão de lookupStoreDetail para os bônus aleatórios,
// que vivem em outra rota (o detalhe do ITEM, não o da loja) e por isso têm
// memo próprio. Mesmo contrato de três estados, e mesmo motivo para o
// orçamento: cada anúncio novo é uma requisição.
func (h *Handler) lookupItemBonus(r *http.Request, item gnjoy.ShopListItem, budget *int) (bonus []string, known, affordable bool) {
	if cached, ok := h.itemDetailMemo.peek(storeKey(item)); ok {
		return cached.RandomOptions(), true, true
	}
	if *budget == 0 {
		return nil, false, false
	}
	*budget--
	bonus, err := h.lookupBonus(r.Context(), item)
	if err != nil {
		return nil, false, true
	}
	return bonus, true, true
}

// watchlistPriceForCheapest também busca o detalhe da loja mais barata —
// mesmo quando o item não é equipamento e por isso não precisa de refino —
// porque é dali que sai a localização (NaviCommand): o card só mostra o
// botão de copiar quando o front-end decide que vale a pena (alvo atingido
// ou item disponível), mas o servidor não sabe qual é o alvo do usuário (ele
// vive só no navegador — ver watchlist.js), então busca sempre que encontra
// um anúncio. O custo real fica baixo na prática: a mesma loja tende a
// continuar sendo a mais barata de uma checagem para a próxima, e a segunda
// em diante é só um peek no memo (ver lookupStoreDetail).
func (h *Handler) watchlistPriceForCheapest(r *http.Request, candidates []gnjoy.ShopListItem) watchlistPriceView {
	if len(candidates) == 0 {
		return watchlistPriceView{Found: false}
	}
	cheapest := candidates[0]
	view := watchlistPriceView{Found: true, MinPrice: cheapest.ItemPrice}

	budget := 1
	if detail, _ := h.lookupStoreDetail(r, cheapest, &budget); detail != nil {
		view.NaviCommand = naviCommand(detail.MapName, detail.Xpos, detail.Ypos)
		if isEquipment(cheapest.DatabaseType) {
			refine := detail.Refine
			view.Refine = &refine
		}
	}
	return view
}

// watchlistPriceForFilter procura o anúncio mais barato que satisfaz o que a
// entrada da watchlist fixou — o refino, os bônus aleatórios, ou os dois.
//
// A varredura é por preço crescente e para no primeiro candidato que serve —
// os mais caros nem chegam a ser consultados. O orçamento (maxDetailFetches)
// limita quantas consultas AINDA NÃO FEITAS uma checagem gasta; o que sobrar
// fica para os próximos ciclos, que encontram nos memos o que este descobriu.
func (h *Handler) watchlistPriceForFilter(r *http.Request, candidates []gnjoy.ShopListItem, filter watchlistFilter) watchlistPriceView {
	budget := maxDetailFetches
	parcial := false

	for _, candidate := range candidates {
		// Carta, consumível e material nunca têm refino nem bônus: descartar
		// aqui é de graça, e numa busca ampla como "Espada" isso é metade dos
		// resultados (mesmo raciocínio de scanItemFacts, em bonus.go).
		if !isEquipment(candidate.DatabaseType) {
			continue
		}

		// O refino vem primeiro de propósito: reprovar por ele dispensa a
		// consulta de bônus deste candidato, que é uma requisição a mais em
		// outra rota.
		var store *gnjoy.StoreDetail
		if filter.Refine != nil {
			detail, affordable := h.lookupStoreDetail(r, candidate, &budget)
			if !affordable {
				// Orçamento esgotado: este anúncio fica para o próximo ciclo
				// (o memo guarda os já descobertos, então lá o orçamento
				// avança para anúncios realmente novos). A varredura segue:
				// um candidato mais caro pode já estar memoizado e responder
				// agora.
				parcial = true
				continue
			}
			if detail == nil || detail.Refine != *filter.Refine {
				continue
			}
			store = detail
		}

		if len(filter.Bonus) > 0 {
			bonus, known, affordable := h.lookupItemBonus(r, candidate, &budget)
			if !affordable {
				parcial = true
				continue
			}
			if !known || !bonusMatches(bonus, filter.Bonus) {
				continue
			}
		}

		// Achou. A localização só existe no detalhe da LOJA, então quando o
		// filtro era só de bônus ela ainda não foi consultada — vale uma
		// requisição a mais, fora do orçamento da varredura, pelo mesmo
		// motivo de watchlistPriceForCheapest: é o anúncio que o usuário vai
		// visitar. Sem ela, a linha aparece sem o botão de localização, que
		// já é um estado tolerado.
		if store == nil {
			extra := 1
			store, _ = h.lookupStoreDetail(r, candidate, &extra)
		}
		view := watchlistPriceView{Found: true, MinPrice: candidate.ItemPrice, Partial: parcial}
		if store != nil {
			refine := store.Refine
			view.Refine = &refine
			view.NaviCommand = naviCommand(store.MapName, store.Xpos, store.Ypos)
		}
		return view
	}

	// Não achou dentro do que os memos e o orçamento deste ciclo alcançaram.
	// Com parcial, pode existir um anúncio que sirva entre os que não chegaram
	// a ser consultados — os próximos ciclos ampliam a cobertura até lá.
	return watchlistPriceView{Found: false, Partial: parcial}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("web: falha ao codificar resposta JSON", "error", err)
	}
}
