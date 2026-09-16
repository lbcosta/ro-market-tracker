package web

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// candidatoView é um item que casou com o nome digitado no estoque. A busca
// por nome casa por TRECHO — "espada" traz "Espada Primordial", "Espada
// Citadina" e "Carta Peixe-Espada" —, então validar devolve uma lista e é o
// usuário quem diz qual é o dele. É o mesmo problema que a tabela de
// histórico já resolve do mesmo jeito (ver history.go).
//
// Os dois caminhos de validação sabem coisas diferentes sobre o item, e
// InMarket é o que diz ao navegador qual conjunto de campos veio preenchido.
type candidatoView struct {
	ItemID int `json:"itemId"`

	// ItemName é o nome de tela. Vindo do mercado ele traz o sufixo de slots
	// (DisplayName), o que separa "Selo de Loki" de "Selo de Loki [1]" —
	// itens de catálogo diferentes, com preços muito diferentes. Vindo do
	// histórico o sufixo não existe, e aí o que os separa na tela é o itemId
	// e a faixa de preço.
	ItemName string `json:"itemName"`

	// SearchName é o nome de catálogo, sem os slots: é ele que volta ao
	// upstream nas consultas seguintes. A busca casa contra o nome cru do
	// anúncio, então procurar por "Selo de Loki [1]" não acharia nada.
	SearchName string `json:"searchName"`

	// SvrID é o id NUMÉRICO do servidor (303 para NIDHOGG). Ele não é
	// derivável do nome e só chega em respostas do site — e o histórico de
	// preço de um item (Fatia seguinte) o exige. Por isso ele é guardado na
	// validação, e não adivinhado depois.
	SvrID int `json:"svrId"`

	DatabaseType string `json:"databaseType"`
	ImgPath      string `json:"imgPath,omitempty"`

	// InMarket diz de qual caminho este candidato veio: true quando alguém
	// está anunciando o item agora (e aí MinPrice e Units valem), false
	// quando ele só foi encontrado no histórico de vendas (e aí valem Min,
	// Avg, Max e Vol). Não é detalhe de implementação vazando: "ninguém está
	// anunciando isto agora" é informação útil para quem vai vender.
	InMarket bool `json:"inMarket"`

	// Do mercado atual.
	MinPrice int64 `json:"minPrice,omitempty"`
	Units    int   `json:"units,omitempty"`

	// Do histórico de vendas.
	Min int64 `json:"min,omitempty"`
	Avg int64 `json:"avg,omitempty"`
	Max int64 `json:"max,omitempty"`
	Vol int   `json:"vol,omitempty"`
}

type validarView struct {
	Candidates []candidatoView `json:"candidates"`

	// Message só é preenchida quando não houve candidato nenhum, e explica
	// POR QUE não houve. É a diferença entre "este item não existe" e
	// "não consegui perguntar" — a segunda nunca chega aqui, responde erro
	// HTTP (ver EstoqueValidar).
	Message string `json:"message,omitempty"`
}

// EstoqueValidar trata GET /web/estoque/validar e responde quais itens do
// servidor casam com o nome que o usuário digitou no estoque.
//
// A ordem das duas consultas não é arbitrária. Primeiro o MERCADO: são os
// anúncios da concorrência, é o caminho comum (um item que alguém vende,
// alguém compra), e é o único que traz o nome com o sufixo de slots. Só se
// ninguém no servidor inteiro estiver anunciando o item é que vale a pena
// gastar a segunda consulta no HISTÓRICO de vendas — porque "ninguém está
// anunciando agora" não quer dizer "não existe", e é justamente o caso de
// quem vai colocar esse item à venda. Se as duas vierem vazias, aí sim o
// item nunca foi negociado no servidor.
//
// É a mesma cadeia que a busca da página principal já faz (ver Search), com
// uma diferença: aqui o fallback chama searchMarketPrice direto, com a janela
// ALL, em vez de priceHistory. A pergunta desta rota é só "este item
// existe?", e ALL responde sozinha — priceHistory consultaria DUAS janelas
// para montar uma tabela de preços que ninguém vai mostrar aqui.
//
// Custo: 1 requisição no caminho comum, 2 quando o item não está no mercado.
func (h *Handler) EstoqueValidar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := q.Get("server")
	item := q.Get("item")
	if server == "" || item == "" {
		http.Error(w, "os parâmetros 'server' e 'item' são obrigatórios", http.StatusBadRequest)
		return
	}

	anuncios, err := h.cachedSearchShops(r.Context(), server, item, freshMaxAge)
	if err != nil {
		escreverErroDeConsulta(w, err, "validação do estoque: busca no mercado falhou", item, server)
		return
	}

	if len(anuncios.Items) > 0 {
		writeJSON(w, http.StatusOK, validarView{Candidates: candidatosDoMercado(anuncios.Items)})
		return
	}

	historico, err := h.searchMarketPrice(r.Context(), server, item, gnjoy.MarketPricePeriodAll)
	if err != nil {
		escreverErroDeConsulta(w, err, "validação do estoque: consulta ao histórico falhou", item, server)
		return
	}

	if len(historico.Items) == 0 {
		writeJSON(w, http.StatusOK, validarView{
			Candidates: []candidatoView{},
			Message: "Nenhum item chamado «" + item + "» está à venda ou já foi vendido no servidor " +
				server + ". Confira o nome e cadastre de novo.",
		})
		return
	}

	writeJSON(w, http.StatusOK, validarView{Candidates: candidatosDoHistorico(historico.Items)})
}

// escreverErroDeConsulta separa "não achei" de "não consegui perguntar". A
// diferença é o que decide se o navegador marca o item como inválido — o
// estado que manda o usuário apagar o cadastro — ou se o deixa como está para
// tentar de novo. Um timeout ou um bloqueio do site NUNCA podem invalidar um
// cadastro correto, e é por isso que estes caminhos respondem erro HTTP em
// vez de uma lista vazia.
func escreverErroDeConsulta(w http.ResponseWriter, err error, msgLog, item, server string) {
	// "Tente de novo em instantes" seria um mau conselho quando o site está
	// limitando as consultas: tentar de novo é justamente o que não se deve
	// fazer, e o aviso no topo da página já diz o que está acontecendo.
	if errors.Is(err, gnjoy.ErrSuspended) {
		http.Error(w,
			"As consultas estão suspensas: o site limitou o acesso. A validação volta assim que ele liberar.",
			http.StatusServiceUnavailable)
		return
	}
	slog.Error("web: "+msgLog, "item", item, "servidor", server, "error", err)
	http.Error(w, "não foi possível consultar o mercado agora", http.StatusBadGateway)
}

// candidatosDoMercado agrupa os anúncios por item: a busca devolve uma linha
// por anúncio, e três lojas vendendo a mesma espada são UM candidato, não
// três. De cada grupo saem o menor preço anunciado e o total de unidades à
// venda — os dois de graça, porque já vêm em cada linha da busca.
func candidatosDoMercado(items []gnjoy.ShopListItem) []candidatoView {
	porItem := make(map[int]*candidatoView, len(items))
	for _, it := range items {
		c, visto := porItem[it.ItemId]
		if !visto {
			porItem[it.ItemId] = &candidatoView{
				ItemID:       it.ItemId,
				ItemName:     it.DisplayName(),
				SearchName:   it.ItemName,
				SvrID:        it.SvrId,
				DatabaseType: it.DatabaseType,
				ImgPath:      it.DatabaseImgPath,
				InMarket:     true,
				MinPrice:     it.ItemPrice,
				Units:        it.ItemCnt,
			}
			continue
		}
		if it.ItemPrice < c.MinPrice {
			c.MinPrice = it.ItemPrice
		}
		c.Units += it.ItemCnt
	}
	return ordenarCandidatos(porItem)
}

func candidatosDoHistorico(items []gnjoy.MarketPriceItem) []candidatoView {
	porItem := make(map[int]*candidatoView, len(items))
	for _, it := range items {
		// O histórico já vem agregado por item pelo próprio site, então aqui
		// não há o que somar — o mapa existe só para o resultado sair pela
		// mesma ordenação do outro caminho.
		porItem[it.ItemId] = &candidatoView{
			ItemID:       it.ItemId,
			ItemName:     it.ItemName,
			SearchName:   it.ItemName,
			SvrID:        it.SvrId,
			DatabaseType: it.DatabaseType,
			ImgPath:      it.DatabaseImgPath,
			InMarket:     false,
			Min:          it.MinItemPrice,
			Avg:          it.AvgItemPrice,
			Max:          it.MaxItemPrice,
			Vol:          it.TotalItemCnt,
		}
	}
	return ordenarCandidatos(porItem)
}

// ordenarCandidatos dá uma ordem estável à lista: por nome, e o itemId
// desempata. Iterar um map em Go tem ordem aleatória, e uma lista de escolha
// que troca de ordem a cada validação seria hostil — ainda mais quando dois
// candidatos têm o MESMO nome, que é exatamente o caso do histórico, onde o
// sufixo de slots não existe.
func ordenarCandidatos(porItem map[int]*candidatoView) []candidatoView {
	lista := make([]candidatoView, 0, len(porItem))
	for _, c := range porItem {
		lista = append(lista, *c)
	}
	slices.SortFunc(lista, func(a, b candidatoView) int {
		if n := cmp.Compare(a.ItemName, b.ItemName); n != 0 {
			return n
		}
		return cmp.Compare(a.ItemID, b.ItemID)
	})
	return lista
}

// maxAnunciosPorItem limita quantos anúncios a rota de mercado devolve por
// item. Ordenados do mais barato para o mais caro, então o corte só descarta
// o que ninguém vai olhar: quem vende precisa saber por quanto o concorrente
// mais barato está vendendo, não o trigésimo. O teto existe porque esta lista
// é guardada no localStorage de cada card, e um item popular com centenas de
// anúncios encheria a cota do navegador sozinho.
const maxAnunciosPorItem = 60

// anuncioView é um anúncio de um item no mercado agora.
//
// O vendedor vai junto de propósito: é o navegador quem decide quais anúncios
// são do próprio usuário, comparando com a lista de personagens dele. Fazer
// esse desconto aqui no servidor obrigaria a mandar a lista de personagens em
// cada consulta — e, pior, editá-la custaria UMA REQUISIÇÃO POR ITEM do
// estoque, porque todo card precisaria recalcular. Do jeito atual, mexer na
// lista é instantâneo e de graça para a tela inteira.
type anuncioView struct {
	Price     int64  `json:"price"`
	Units     int    `json:"units"`
	StoreName string `json:"storeName,omitempty"`
	Seller    string `json:"seller,omitempty"`
}

type mercadoView struct {
	// Found diz se existe ALGUM anúncio do item agora, do usuário ou de
	// terceiros. Quem separa uma coisa da outra é o navegador.
	Found bool `json:"found"`

	// DisplayName é o nome com o sufixo de slots. Vem junto porque um item
	// validado pelo histórico não tinha como saber o sufixo (ver
	// candidatoView.ItemName) — a primeira consulta de mercado corrige isso
	// sem custar requisição nenhuma.
	DisplayName string `json:"displayName,omitempty"`

	Listings []anuncioView `json:"listings"`

	// Truncated avisa que havia mais anúncios do que maxAnunciosPorItem. A
	// tela não mente sobre o que não olhou.
	Truncated bool `json:"truncated,omitempty"`
}

// EstoqueMercado trata GET /web/estoque/mercado e devolve os anúncios do item
// no mercado agora — o que a concorrência está pedindo por ele.
//
// Custo: 1 requisição, ou 0 quando o cache responde. Não chama
// GetStoreDetail: a watchlist gasta uma requisição extra por consulta só para
// obter o /navi da loja mais barata, e quem tem o item no estoque não vai a
// lugar nenhum — vai comparar preço. O nome da loja, que é o que interessa
// aqui, já vem de graça em cada linha da busca.
func (h *Handler) EstoqueMercado(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := q.Get("server")
	item := q.Get("item")
	itemID, err := strconv.Atoi(q.Get("itemId"))
	if server == "" || item == "" || err != nil {
		http.Error(w, "os parâmetros 'server', 'item' e 'itemId' são obrigatórios", http.StatusBadRequest)
		return
	}

	// maxAge alto por padrão (o mesmo da watchlist): várias abas e vários
	// cards do mesmo item não multiplicam o tráfego. fresh=1 é o botão "↻",
	// onde o usuário pediu explicitamente o estado de agora.
	maxAge := monitorMaxAge
	if q.Get("fresh") == "1" {
		maxAge = 0
	}

	// NoRetry: esta consulta tem repetição própria (o usuário clica de novo,
	// ou o rodízio volta nela), e insistir aqui disputaria a cota com as
	// ações que alguém está esperando na tela.
	result, err := h.cachedSearchShops(r.Context(), server, item, maxAge, gnjoy.NoRetry())
	if err != nil {
		escreverErroDeConsulta(w, err, "estoque: consulta ao mercado falhou", item, server)
		return
	}

	// A busca por nome casa por trecho e traz itens diferentes; só as linhas
	// deste itemId interessam (o mesmo filtro que a watchlist faz).
	anuncios := make([]gnjoy.ShopListItem, 0, len(result.Items))
	for _, it := range result.Items {
		if it.ItemId == itemID {
			anuncios = append(anuncios, it)
		}
	}
	if len(anuncios) == 0 {
		writeJSON(w, http.StatusOK, mercadoView{Found: false, Listings: []anuncioView{}})
		return
	}

	slices.SortFunc(anuncios, func(a, b gnjoy.ShopListItem) int {
		return cmp.Compare(a.ItemPrice, b.ItemPrice)
	})

	view := mercadoView{Found: true, DisplayName: anuncios[0].DisplayName()}
	if len(anuncios) > maxAnunciosPorItem {
		anuncios = anuncios[:maxAnunciosPorItem]
		view.Truncated = true
	}
	view.Listings = make([]anuncioView, 0, len(anuncios))
	for _, it := range anuncios {
		view.Listings = append(view.Listings, anuncioView{
			Price:     it.ItemPrice,
			Units:     it.ItemCnt,
			StoreName: it.StoreName,
			Seller:    it.ItemSellerCharName,
		})
	}
	writeJSON(w, http.StatusOK, view)
}

// Janelas do histórico oferecidas no card. Os rótulos são os mesmos do
// seletor no navegador (ver JANELAS em static/estoque.js).
const (
	janelaDia   = "1"
	janelaSete  = "7"
	janelaMes   = "30"
	janelaTudo  = "ALL"
	janelaSonda = 30 // quantos dias pedir só para descobrir quantos existem

	// maxDiasDoHistorico é o teto da janela "todo o histórico". O site devolve
	// quantos dias existem (PriceDayStat.TotalCount), mas um item negociado há
	// anos poderia pedir uma página enorme — e nem o tamanho que o site aceita
	// nem o custo disso foram medidos contra o real. O teto deixa a janela
	// "tudo" ser melhor esforço em vez de uma aposta.
	maxDiasDoHistorico = 365
)

// limiteDaJanela traduz a janela escolhida no card para o "limit" da consulta
// de histórico.
//
// É o limit, e NÃO o period, que implementa o seletor: o site pagina a série
// diária por ele (comprovado em docs/webtools-api-research.md), enquanto o
// period desta Server Action nunca foi observado — a captura original mandava
// o literal "$undefined" do Next.js (ver gnjoy.PriceHistoryParams.Period).
// Depender dele seria depender de comportamento que ninguém confirmou.
//
// A janela "tudo" devolve 0: quantos dias existem só se descobre perguntando.
func limiteDaJanela(janela string) (int, bool) {
	switch janela {
	case janelaDia:
		return 1, true
	case janelaSete:
		return 7, true
	case janelaMes:
		return 30, true
	case janelaTudo:
		return 0, true
	default:
		return 0, false
	}
}

type diaView struct {
	Date string `json:"date"`
	Min  int64  `json:"min"`
	Avg  int64  `json:"avg"`
	Max  int64  `json:"max"`
	Qty  int    `json:"qty"`
}

type resumoView struct {
	Days        int     `json:"days"`
	Min         int64   `json:"min"`
	Max         int64   `json:"max"`
	WeightedAvg float64 `json:"weightedAvg"`
	StdDev      float64 `json:"stdDev"`
	QtySold     int     `json:"qtySold"`

	// MedianMin, MedianMax e Dispersao são o que o navegador usa para
	// precificar equipamento, onde a média é enganosa porque o histórico
	// mistura unidades com refinos e encantamentos diferentes. Ver periodStats.
	MedianMin int64   `json:"medianMin"`
	MedianMax int64   `json:"medianMax"`
	Dispersao float64 `json:"dispersion"`
}

type historicoView struct {
	Window string `json:"window"`

	// DaysAvailable é quantos dias de venda existem no total, e não quantos
	// vieram nesta janela. É o que permite ao card dizer "7 de 32 dias" — e
	// é o único jeito honesto de montar a janela "tudo".
	DaysAvailable int `json:"daysAvailable"`

	// Days vai completa, sem corte. O site só devolve dias que TIVERAM venda,
	// então cada linha é um dia em que o item de fato trocou de mãos — e são
	// esses números que vão alimentar a fórmula de preço sugerido.
	Days    []diaView  `json:"days"`
	Summary resumoView `json:"summary"`
}

// EstoqueHistorico trata GET /web/estoque/historico e devolve a série diária
// de vendas do item na janela pedida.
//
// Custo: 1 requisição (0 quando o cache de historyMaxAge responde). A janela
// "todo o histórico" pode custar 2 na primeira vez de um item: o limit precisa
// de um número, e quantos dias existem só se sabe perguntando — a primeira
// consulta traz o TotalCount, a segunda busca o total. Da segunda vez em
// diante o cache tem a resposta.
func (h *Handler) EstoqueHistorico(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	itemID, errItem := strconv.Atoi(q.Get("itemId"))
	svrID, errSvr := strconv.Atoi(q.Get("svrId"))
	janela := q.Get("janela")
	limite, janelaOK := limiteDaJanela(janela)
	if errItem != nil || errSvr != nil || !janelaOK {
		http.Error(w,
			"os parâmetros 'itemId', 'svrId' e 'janela' (1, 7, 30 ou ALL) são obrigatórios",
			http.StatusBadRequest)
		return
	}

	maxAge := historyMaxAge
	if q.Get("fresh") == "1" {
		maxAge = 0
	}

	if limite == 0 {
		// Janela "tudo": sonda primeiro para descobrir quantos dias existem.
		// A sonda usa um limit fixo (e não 1) porque, quando o item tem menos
		// dias que isso, ela já é a resposta completa e a segunda consulta
		// nem acontece.
		sonda, err := h.historicoCacheado(r.Context(), svrID, itemID, janelaSonda, maxAge)
		if err != nil {
			escreverErroDeConsulta(w, err, "estoque: consulta ao histórico falhou", q.Get("itemId"), "")
			return
		}
		total := diasDisponiveis(sonda)
		if total <= len(sonda.DayStatsList) {
			writeJSON(w, http.StatusOK, montarHistorico(janela, total, sonda))
			return
		}
		limite = min(total, maxDiasDoHistorico)
	}

	history, err := h.historicoCacheado(r.Context(), svrID, itemID, limite, maxAge)
	if err != nil {
		escreverErroDeConsulta(w, err, "estoque: consulta ao histórico falhou", q.Get("itemId"), "")
		return
	}
	writeJSON(w, http.StatusOK, montarHistorico(janela, diasDisponiveis(history), history))
}

// diasDisponiveis lê o total de dias que o site conhece. Ele vem repetido em
// cada linha devolvida (é assim no site), então qualquer uma serve; sem
// nenhuma linha, o que veio é o que existe.
func diasDisponiveis(history *gnjoy.PriceHistory) int {
	if len(history.DayStatsList) == 0 {
		return 0
	}
	if total := history.DayStatsList[0].TotalCount; total > 0 {
		return total
	}
	return len(history.DayStatsList)
}

func montarHistorico(janela string, total int, history *gnjoy.PriceHistory) historicoView {
	dias := make([]diaView, 0, len(history.DayStatsList))
	for _, d := range history.DayStatsList {
		dias = append(dias, diaView{
			Date: d.Date,
			Min:  d.MinItemPrice,
			Avg:  d.AvgItemPrice,
			Max:  d.MaxItemPrice,
			Qty:  d.ItemCnt,
		})
	}
	resumo := computePeriodStats(history.DayStatsList)
	return historicoView{
		Window:        janela,
		DaysAvailable: total,
		Days:          dias,
		Summary: resumoView{
			Days:        resumo.Days,
			Min:         resumo.Min,
			Max:         resumo.Max,
			WeightedAvg: resumo.WeightedAvg,
			StdDev:      resumo.StdDev,
			QtySold:     resumo.QtySold,
			MedianMin:   resumo.MedianMin,
			MedianMax:   resumo.MedianMax,
			Dispersao:   resumo.Dispersao,
		},
	}
}

// historicoCacheado é a consulta de histórico passando pelo cache. Roda
// desacoplada do cancelamento de quem a disparou, como as outras consultas
// compartilhadas, e devolve um clone: quem chama recorta a série à vontade
// sem corromper o exemplar guardado.
func (h *Handler) historicoCacheado(ctx context.Context, svrID, itemID, limite int, maxAge time.Duration) (*gnjoy.PriceHistory, error) {
	key := cacheKey(strconv.Itoa(svrID), strconv.Itoa(itemID), strconv.Itoa(limite))
	history, err := h.priceHistoryCache.Do(key, maxAge, func() (*gnjoy.PriceHistory, error) {
		return h.client.GetPriceHistory(context.WithoutCancel(ctx), gnjoy.PriceHistoryParams{
			ItemId: itemID,
			SvrId:  svrID,
			Limit:  limite,
		}, gnjoy.NoRetry())
	})
	if err != nil {
		return nil, err
	}
	clone := *history
	clone.DayStatsList = slices.Clone(history.DayStatsList)
	return &clone, nil
}
