package web

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// historyDays é a janela recente do resumo de preços praticados mostrado
// quando o item não está no mercado: os últimos 7 dias, o mesmo recorte do
// card de detalhe de um anúncio.
const historyDays = 7

// historyRow é uma linha da tabela: o resumo de um item e a janela de onde os
// números vieram.
type historyRow struct {
	gnjoy.MarketPriceItem

	// Recent diz que os números são dos últimos historyDays dias. Quando
	// falso, são de todo o histórico — o item existe, mas não teve venda
	// nenhuma na janela recente.
	Recent bool
}

// historyView é o fragmento renderizado quando a busca não achou nenhum
// anúncio do item no mercado atual. Só um entre Error, Message e a tabela de
// Rows aparece:
//
//   - Error: a consulta ao upstream falhou (mensagem em vermelho);
//   - Message: o item nunca foi vendido no servidor;
//   - caso contrário, uma linha por item que casou com o termo buscado.
type historyView struct {
	Error   string
	Message string
	Server  string
	Query   string
	Days    int
	Rows    []historyRow

	// AllHistoric e Mixed resumem de quais janelas as linhas vieram. O
	// template usa isso para dizer o período uma vez só, no texto acima da
	// tabela, quando todas as linhas concordam — e por linha, em uma coluna
	// a mais, apenas quando elas divergem, que é quando essa coluna carrega
	// alguma informação.
	AllHistoric bool
	Mixed       bool
}

// priceHistory monta o que mostrar para um item que a busca não encontrou no
// mercado atual: por quanto cada item que casou com o termo andou sendo
// vendido no servidor, ou o aviso de que nada disso nunca foi vendido lá.
//
// Os números vêm prontos do site (SearchMarketPrice), que agrega mínimo,
// médio, máximo e volume por item no período pedido. A busca é por nome e
// casa por trecho, então o resultado costuma ter mais de uma linha —
// "rapidez" traz "Módulo de S-Rapidez" e "Automódulo de M-Rapidez", e só
// quem procurou sabe qual dos dois queria.
//
// São duas consultas, uma por janela, porque elas respondem coisas
// diferentes: o histórico completo diz QUAIS itens existem, e a janela de
// historyDays dias diz quanto eles custam HOJE.
func (h *Handler) priceHistory(ctx context.Context, server, item string) historyView {
	view := historyView{Server: server, Query: item, Days: historyDays}

	// O histórico completo vem primeiro porque é ele que define a lista: um
	// item sem venda na última semana não aparece na janela curta, e montar a
	// tabela a partir dela faria esse item sumir da tela sem o usuário ter
	// como saber que ele existe. Foi exatamente o que acontecia com
	// "reformador primordial ii", que não vendia havia mais de uma semana
	// enquanto o "III" vendia.
	full, err := h.searchMarketPrice(ctx, server, item, gnjoy.MarketPricePeriodAll)
	if err != nil {
		view.Error = "Não foi possível consultar o histórico de preços agora. Tente novamente em instantes."
		return view
	}
	if len(full.Items) == 0 {
		view.Message = fmt.Sprintf("“%s” não está anunciado no mercado agora e nunca foi vendido no servidor %s — por isso não há nenhum preço registrado para esse item.", item, server)
		return view
	}

	// A janela recente refina os números: em um mercado volátil, o mínimo de
	// todo o histórico diz pouco sobre quanto o item custa hoje. Se ela
	// falhar, a tabela ainda sai inteira com os números do histórico completo
	// — cada linha diz de qual janela veio, então isso não engana ninguém.
	recent, err := h.searchMarketPrice(ctx, server, item, gnjoy.MarketPricePeriodWeek)
	if err != nil {
		recent = &gnjoy.MarketPriceResult{}
	}

	view.Rows = mergeHistoryRows(full.Items, recent.Items)
	recentCount := 0
	for _, row := range view.Rows {
		if row.Recent {
			recentCount++
		}
	}
	view.AllHistoric = recentCount == 0
	view.Mixed = recentCount > 0 && recentCount < len(view.Rows)
	return view
}

// mergeHistoryRows monta uma linha por item do histórico completo, preferindo
// os números da janela recente para os itens que venderam nela.
//
// Um item que apareça só na janela recente (o upstream se contradizendo entre
// as duas consultas, ou uma venda acontecendo entre elas) também entra: a
// regra desta função é não perder item nenhum, que é justamente o defeito que
// ela existe para consertar.
func mergeHistoryRows(full, recent []gnjoy.MarketPriceItem) []historyRow {
	recentByID := make(map[int]gnjoy.MarketPriceItem, len(recent))
	for _, item := range recent {
		recentByID[item.ItemId] = item
	}

	rows := make([]historyRow, 0, len(full))
	listed := make(map[int]bool, len(full))
	for _, item := range full {
		listed[item.ItemId] = true
		if fresh, ok := recentByID[item.ItemId]; ok {
			rows = append(rows, historyRow{MarketPriceItem: fresh, Recent: true})
			continue
		}
		rows = append(rows, historyRow{MarketPriceItem: item})
	}
	for _, item := range recent {
		if !listed[item.ItemId] {
			rows = append(rows, historyRow{MarketPriceItem: item, Recent: true})
		}
	}
	return rows
}

func (h *Handler) searchMarketPrice(ctx context.Context, server, item, period string) (*gnjoy.MarketPriceResult, error) {
	result, err := h.client.SearchMarketPrice(ctx, gnjoy.MarketPriceParams{
		ServerType: server,
		SearchWord: item,
		Period:     period,
	})
	if err != nil {
		slog.Error("web: consulta de preços praticados falhou",
			"item", item, "servidor", server, "período", period, "error", err)
		return nil, err
	}
	return result, nil
}
