package web

import (
	"cmp"
	"errors"
	"log/slog"
	"net/http"
	"slices"

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
