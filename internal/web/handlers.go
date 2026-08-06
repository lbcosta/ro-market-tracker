package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type Handler struct {
	client *gnjoy.Client

	// warmupOnce garante que o aquecimento do action id (ver warmupActionID)
	// só dispare uma vez por processo, mesmo que a página seja recarregada
	// várias vezes.
	warmupOnce sync.Once

	// searchCache guarda resultados de busca de lojas por (servidor, termo)
	// e marketPriceCache os resumos de preços praticados por (servidor,
	// termo, período) — ver cachedSearchShops e history.go. São eles que
	// impedem o tráfego redundante do navegador (abas, F5, reordenação,
	// ciclo da watchlist) de virar requisição nova ao GnJoy.
	searchCache      *ttlCache[*gnjoy.ShopSearchResult]
	marketPriceCache *ttlCache[*gnjoy.MarketPriceResult]

	// storeDetailMemo e itemDetailMemo cacheiam o detalhe COMPLETO de um
	// anúncio por (svrId, mapId, ssi) — não só o refino ou os bônus extraídos
	// dele. O detalhe nunca muda para um mesmo ssi (a loja fechada e reaberta
	// ganha um ssi novo), então cada anúncio custa no máximo UMA consulta de
	// cada na vida do processo, e essa consulta é COMPARTILHADA entre todo
	// caminho que precisa dela: a varredura de refino/bônus da busca
	// (bonus.go), o orçamento da watchlist (watchlist.go) e o card de
	// detalhe ao expandir uma linha (Expand, abaixo). Antes de existirem
	// estes dois memos, cada um desses três caminhos guardava só o valor
	// derivado (o int do refino, a lista de bônus) em caches separados — e
	// clicar para expandir uma linha já verificada pela varredura refazia as
	// duas consultas do zero, porque não havia onde procurar o detalhe
	// completo que o Expand precisa (nome da loja, vendedor, localização...).
	storeDetailMemo *ttlCache[*gnjoy.StoreDetail]
	itemDetailMemo  *ttlCache[*gnjoy.ItemDetail]
}

func NewHandler(client *gnjoy.Client) *Handler {
	return &Handler{
		client:           client,
		searchCache:      newTTLCache[*gnjoy.ShopSearchResult](searchCacheSize),
		marketPriceCache: newTTLCache[*gnjoy.MarketPriceResult](marketPriceCacheSize),
		storeDetailMemo:  newTTLCache[*gnjoy.StoreDetail](storeDetailMemoSize),
		itemDetailMemo:   newTTLCache[*gnjoy.ItemDetail](itemDetailMemoSize),
	}
}

// cachedSearchShops é o único caminho pelo qual o frontend busca lojas no
// upstream: consultas idênticas (mesmo servidor e termo) dentro de maxAge
// são servidas do cache, e as concorrentes são deduplicadas — é o que torna
// inofensivos os multiplicadores de tráfego do navegador, que afunilam todos
// aqui. Só busca lojas comprando o item (anúncios de venda de jogadores, o
// que interessa a quem rastreia preços); não há seletor de tipo na UI.
//
// A busca compartilhada roda desacoplada do cancelamento da requisição que a
// disparou (context.WithoutCancel): o resultado alimenta o cache e serve às
// próximas consultas, então um F5 no meio dela não deve derrubá-la.
func (h *Handler) cachedSearchShops(ctx context.Context, server, item string, maxAge time.Duration, opts ...gnjoy.CallOption) (*gnjoy.ShopSearchResult, error) {
	key := cacheKey(server, string(gnjoy.StoreTypeBuy), item)
	result, err := h.searchCache.Do(key, maxAge, func() (*gnjoy.ShopSearchResult, error) {
		return h.client.SearchShops(context.WithoutCancel(ctx), gnjoy.SearchShopsParams{
			ServerType: server,
			StoreType:  gnjoy.StoreTypeBuy,
			SearchWord: item,
		}, opts...)
	})
	if err != nil {
		return nil, err
	}
	// Cópia rasa: quem chama reordena e filtra a lista à vontade sem
	// corromper o exemplar compartilhado que continua no cache.
	return &gnjoy.ShopSearchResult{
		Items:      slices.Clone(result.Items),
		TotalCount: result.TotalCount,
	}, nil
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

// ResetCaches trata POST /web/cache/reset e esvazia os caches de consulta do
// frontend (busca de lojas, preços praticados e os memos de detalhe).
//
// Existe para os testes de navegador (e2e): o servidor sobe uma vez para a
// suíte inteira, e cada teste zera o site falso e o localStorage para
// começar do mesmo estado — sem zerar TAMBÉM este cache, um teste enxergaria
// o mercado que o teste anterior deixou cacheado. Fora dos testes é
// inofensivo: o pior que a rota faz é a próxima consulta ir ao upstream de
// verdade.
func (h *Handler) ResetCaches(w http.ResponseWriter, r *http.Request) {
	h.searchCache.reset()
	h.marketPriceCache.reset()
	h.storeDetailMemo.reset()
	h.itemDetailMemo.reset()
	w.WriteHeader(http.StatusNoContent)
}

type resultsView struct {
	Error  string
	Server string
	Query  string
	Items  []gnjoy.ShopListItem
	Groups []resultsGroup

	// Aviso é o que ficou faltando NESTA tabela — hoje, os anúncios que a
	// varredura de detalhes não alcançou. É diferente de Error: a busca deu
	// certo, e o que se mostra é verdadeiro, só incompleto.
	Aviso string

	// GroupByRefine e GroupByBonus dizem se as seções foram separadas por
	// esses critérios. O template lê daqui, e não de cada grupo: é uma
	// propriedade da busca inteira.
	GroupByRefine bool
	GroupByBonus  bool

	SortBy       string
	SortDir      string
	PriceSortURL string
	QtySortURL   string
	PriceArrow   string
	QtyArrow     string

	// VariantsURL carrega as outras versões do item que existem no servidor
	// mas não estão anunciadas agora (ver variants.go). É um link, e não parte
	// desta resposta, porque descobri-las custa outra consulta ao site.
	VariantsURL string
}

// resultsGroup é a seção da tabela de resultados com todos os anúncios de um
// mesmo item de catálogo (mesmo ItemID). Uma busca por palavra pode casar
// vários itens de nomes diferentes (ex.: "Espada" acha "Espada Primordial",
// "Espada Citadina" e "Carta Peixe-Espada"); sem agrupar, um único botão "+
// Watchlist" no topo da tabela não tem como dizer qual desses itens ele
// adiciona.
type resultsGroup struct {
	ItemID int

	// ItemName é o nome como ele aparece na tela: com o sufixo de slots,
	// porque é ele que distingue duas seções que, sem isso, sairiam com o
	// mesmo cabeçalho repetido — "Selo de Loki" e "Selo de Loki [1]" são
	// itens de catálogo diferentes, com preços muito diferentes.
	ItemName string

	// SearchName é o nome de catálogo, sem os slots. É o que a watchlist
	// manda de volta ao upstream ao consultar o preço ao vivo: a busca casa
	// contra o ItemName cru do anúncio, então procurar pelo nome de tela
	// ("Selo de Loki [1]") não acharia anúncio nenhum.
	SearchName string

	// ImgPath é o ícone do item na CDN do GnJoy, que a própria busca já
	// devolve em cada anúncio — mostrá-lo não custa requisição nenhuma a
	// mais ao site, porque quem baixa a imagem é o navegador, direto da
	// CDN, fora do rate limiter do client (ver gnjoy.ShopListItem).
	ImgPath string

	// Refine e Bonus só são preenchidos quando a busca pediu a varredura de
	// detalhes (ver bonus.go). Os campos "Known" distinguem "consultei e é
	// +0" de "não cheguei a consultar" — sem eles a seção "+0" recolheria os
	// anúncios não verificados, que podem ser de qualquer refino.
	Refine      int
	RefineKnown bool
	Bonus       []string
	BonusKnown  bool

	Items []gnjoy.ShopListItem
}

// groupKey identifica uma seção da tabela. Com os dois checkboxes desligados
// facts é nil e a chave colapsa em {itemID}, que é o agrupamento de sempre.
type groupKey struct {
	itemID      int
	refine      int
	refineKnown bool
	bonus       string
	bonusKnown  bool
}

// groupItems agrupa items — já ordenados pela coluna/direção escolhida — por
// item de catálogo e, se a busca pediu, também por refino e pelos bônus
// aleatórios de cada unidade. Preserva a ordem de primeira aparição de cada
// grupo. Como items já chega ordenado e uma subsequência de uma sequência
// ordenada continua ordenada, as linhas dentro de cada grupo saem na mesma
// ordem relativa que teriam na tabela sem agrupamento.
//
// Os anúncios que a varredura não alcançou formam seções próprias, separadas
// das verificadas: eles não são "+0 sem bônus", são desconhecidos.
func groupItems(items []gnjoy.ShopListItem, facts map[string]itemFacts, byRefine, byBonus bool) []resultsGroup {
	groups := make([]resultsGroup, 0, len(items))
	index := make(map[groupKey]int, len(items))
	for _, it := range items {
		f := facts[storeKey(it)]

		key := groupKey{itemID: it.ItemId}
		if byRefine {
			key.refine, key.refineKnown = f.Refine, f.RefineKnown
		}
		if byBonus {
			key.bonus, key.bonusKnown = bonusKey(f.Bonus), f.BonusKnown
		}

		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			g := resultsGroup{
				ItemID:     it.ItemId,
				ItemName:   it.DisplayName(),
				SearchName: it.ItemName,
				ImgPath:    it.DatabaseImgPath,
			}
			if byRefine {
				g.Refine, g.RefineKnown = f.Refine, f.RefineKnown
			}
			if byBonus {
				// Ordem original (opt1..opt4, a do site): só a CHAVE é
				// ordenada, para a mesma combinação em ordens diferentes não
				// virar duas seções.
				g.Bonus, g.BonusKnown = f.Bonus, f.BonusKnown
			}
			groups = append(groups, g)
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

	sq := searchQuery{
		Server:   server,
		Item:     item,
		SortBy:   sortBy,
		SortDir:  sortDir,
		ByRefine: q.Get("refine") == "1",
		ByBonus:  q.Get("bonus") == "1",
	}

	result, err := h.cachedSearchShops(r.Context(), server, item, freshMaxAge)
	if err != nil {
		// "Tente de novo em instantes" seria um mau conselho quando o site
		// está limitando as consultas: tentar de novo é justamente o que não
		// se deve fazer, e o aviso no topo da página já diz o que está
		// acontecendo.
		if errors.Is(err, gnjoy.ErrSuspended) {
			render(w, "results.html.tmpl", resultsView{Error: "As consultas estão suspensas: o site limitou o acesso. A busca volta assim que ele liberar."})
			return
		}
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
		Server:        server,
		Items:         result.Items,
		Query:         item,
		SortBy:        sortBy,
		SortDir:       sortDir,
		GroupByRefine: sq.ByRefine,
		GroupByBonus:  sq.ByBonus,
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

	// A varredura roda DEPOIS da ordenação e antes do agrupamento: se ela
	// parar no meio, o que ficou verificado são os anúncios mais baratos.
	facts, naoVerificados := h.scanItemFacts(r.Context(), result.Items, sq.ByRefine, sq.ByBonus)
	if naoVerificados > 0 {
		view.Aviso = avisoDeVarreduraIncompleta(naoVerificados, sq.ByRefine, sq.ByBonus)
	}

	view.Groups = groupItems(result.Items, facts, sq.ByRefine, sq.ByBonus)
	view.PriceSortURL = sq.sortURL("price")
	view.QtySortURL = sq.sortURL("qty")
	view.PriceArrow = sortArrow(sortBy, sortDir, "price")
	view.QtyArrow = sortArrow(sortBy, sortDir, "qty")
	// As outras versões não têm seção nenhuma para separar, então os
	// checkboxes não vão junto: seriam peso morto na URL.
	view.VariantsURL = variantsURL(server, item, result.Items)
	render(w, "results.html.tmpl", view)
}

// searchQuery são os parâmetros de uma busca. Existe como struct, e não como
// lista de argumentos, porque com servidor, termo, coluna, direção e dois
// booleanos seguidos, trocar dois deles de lugar numa chamada seria questão de
// tempo.
type searchQuery struct {
	Server  string
	Item    string
	SortBy  string
	SortDir string

	// ByRefine e ByBonus ligam a varredura de detalhes (ver bonus.go).
	ByRefine bool
	ByBonus  bool
}

func (q searchQuery) values() url.Values {
	v := url.Values{}
	v.Set("server", q.Server)
	v.Set("item", q.Item)
	v.Set("sort", q.SortBy)
	v.Set("dir", q.SortDir)
	if q.ByRefine {
		v.Set("refine", "1")
	}
	if q.ByBonus {
		v.Set("bonus", "1")
	}
	return v
}

// sortURL monta a URL de /web/search que reordena os resultados por column,
// preservando servidor, termo buscado e a separação por refino/bônus. Clicar
// de novo na mesma coluna já ordenada inverte a direção; clicar em outra
// coluna começa em ordem ascendente.
//
// Carregar os checkboxes junto não é detalhe: o link substitui a tabela
// inteira, e sem eles ela se desagruparia sozinha com as caixas ainda marcadas
// na tela. E como a URL é montada a partir do que ESTA busca pediu — não do
// estado atual do formulário —, quem marca o checkbox depois de buscar e clica
// em "Preço" não dispara uma varredura de um minuto sem querer: para isso
// precisa buscar de novo.
func (q searchQuery) sortURL(column string) string {
	next := q
	next.SortBy = column
	next.SortDir = "asc"
	if q.SortBy == column && q.SortDir == "asc" {
		next.SortDir = "desc"
	}
	return "/web/search?" + next.values().Encode()
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

// itemLang é o idioma pedido ao site nos detalhes de item. Os bônus
// aleatórios da unidade anunciada vêm como frases prontas nesse idioma
// ("CRIT +4", "Conjuração variável -5%") e vão direto para o card de detalhe:
// em inglês, destoariam do resto da página.
const itemLang = "pt-BR"

type expandView struct {
	Error       string
	Store       *gnjoy.StoreDetail
	Item        *gnjoy.ItemDetail
	NaviCommand string
	Stats       sevenDayStats
}

// storeDetail devolve o detalhe completo de um anúncio, do memo ou do
// upstream — e o memoiza para os PRÓXIMOS caminhos que precisarem dele,
// venham eles de onde vierem. É o que faz uma loja já verificada pela
// varredura de refino da busca (bonus.go) ou pelo orçamento da watchlist
// (watchlist.go) não custar uma segunda consulta quando a mesma linha é
// expandida logo em seguida — e vice-versa.
func (h *Handler) storeDetail(ctx context.Context, loc gnjoy.StoreLocation, opts ...gnjoy.CallOption) (*gnjoy.StoreDetail, error) {
	key := locationKey(loc)
	if detail, ok := h.storeDetailMemo.peek(key); ok {
		return detail, nil
	}
	detail, err := h.client.GetStoreDetail(ctx, loc, opts...)
	if err != nil {
		return nil, err
	}
	h.storeDetailMemo.put(key, detail)
	return detail, nil
}

// itemDetail é o equivalente de storeDetail para o detalhe do item — sempre
// no idioma itemLang, porque é dele que saem os bônus aleatórios mostrados no
// card de detalhe, como frases prontas; em outro idioma, destoariam do resto
// da página.
func (h *Handler) itemDetail(ctx context.Context, loc gnjoy.StoreLocation, opts ...gnjoy.CallOption) (*gnjoy.ItemDetail, error) {
	key := locationKey(loc)
	if detail, ok := h.itemDetailMemo.peek(key); ok {
		return detail, nil
	}
	detail, err := h.client.GetItemDetail(ctx, loc, itemLang, opts...)
	if err != nil {
		return nil, err
	}
	h.itemDetailMemo.put(key, detail)
	return detail, nil
}

// Expand trata GET /web/shops/{svrId}/{mapId}/{ssi}/expand: busca o detalhe
// da loja, o detalhe do item e os últimos 7 dias de histórico de preço, e
// devolve o card HTML que o HTMX encaixa na linha de detalhe abaixo do item
// clicado.
//
// Loja e item passam por storeDetail/itemDetail (memoizados), não por uma
// chamada direta ao client: se a busca já tiver verificado refino e/ou bônus
// desse anúncio (checkboxes "Verificar refino"/"Verificar bônus aleatórios"),
// as duas consultas já aconteceram, e expandir a linha reaproveita o que já
// foi pago em vez de refazê-las. O histórico de preço continua sempre fresco
// — não há como esse dado ficar desatualizado de propósito, e ele nunca é
// buscado por nenhum outro caminho.
func (h *Handler) Expand(w http.ResponseWriter, r *http.Request) {
	loc, ok := parseLocation(w, r)
	if !ok {
		return
	}

	store, err := h.storeDetail(r.Context(), loc)
	if err != nil {
		slog.Error("web: detalhe da loja falhou", "error", err)
		render(w, "expand.html.tmpl", expandView{Error: "Não foi possível carregar os detalhes dessa loja agora."})
		return
	}

	item, err := h.itemDetail(r.Context(), loc)
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
