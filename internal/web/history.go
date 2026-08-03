package web

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// historyDays é a janela padrão do resumo de preços praticados mostrado
// quando o item não está no mercado: os últimos 7 dias, o mesmo recorte do
// card de detalhe de um anúncio.
const historyDays = 7

// historyView é o fragmento renderizado quando a busca não achou nenhum
// anúncio do item no mercado atual. Só um entre Error, Message e a tabela de
// Items aparece:
//
//   - Error: a consulta ao upstream falhou (mensagem em vermelho);
//   - Message: o item nunca foi vendido no servidor;
//   - caso contrário, uma linha por item que casou com o termo buscado.
type historyView struct {
	Error   string
	Message string
	Server  string
	Query   string
	Items   []gnjoy.MarketPriceItem

	// Days é a janela consultada (historyDays), na view para o template não
	// repetir o número por conta própria.
	Days int

	// FullHistory diz que a tabela mostra o histórico completo em vez dos
	// últimos historyDays dias, porque não houve venda nenhuma na última
	// semana. A distinção importa: "não vende há mais de uma semana" e "os
	// preços da última semana foram estes" são informações bem diferentes
	// para quem está decidindo quanto pagar.
	FullHistory bool
}

// priceHistory monta o que mostrar para um item que a busca não encontrou no
// mercado atual: por quanto ele andou sendo vendido no servidor, ou o aviso
// de que nunca foi vendido lá.
//
// Os números vêm prontos do site (SearchMarketPrice), que agrega mínimo,
// médio, máximo e volume por item no período pedido. A busca é por nome e
// casa por trecho, então o resultado costuma ter mais de uma linha —
// "rapidez" traz "Módulo de S-Rapidez" e "Automódulo de M-Rapidez", e só
// quem procurou sabe qual dos dois queria.
func (h *Handler) priceHistory(ctx context.Context, server, item string) historyView {
	view := historyView{Server: server, Query: item, Days: historyDays}

	recent, err := h.searchMarketPrice(ctx, server, item, gnjoy.MarketPricePeriodWeek)
	if err != nil {
		view.Error = "Não foi possível consultar o histórico de preços agora. Tente novamente em instantes."
		return view
	}
	if len(recent.Items) > 0 {
		view.Items = recent.Items
		return view
	}

	// Sem vendas na última semana o item pode nunca ter sido vendido, ou
	// apenas estar parado há mais tempo — e a diferença muda a resposta que
	// interessa a quem quer rastreá-lo. Só o histórico completo separa os
	// dois casos, e ele custa uma requisição a mais que não vale a pena pagar
	// quando a janela curta já tem o que mostrar.
	full, err := h.searchMarketPrice(ctx, server, item, gnjoy.MarketPricePeriodAll)
	if err != nil {
		view.Error = "Não foi possível consultar o histórico de preços agora. Tente novamente em instantes."
		return view
	}
	if len(full.Items) == 0 {
		view.Message = fmt.Sprintf("“%s” não está anunciado no mercado agora e nunca foi vendido no servidor %s — por isso não há nenhum preço registrado para esse item.", item, server)
		return view
	}
	view.Items = full.Items
	view.FullHistory = true
	return view
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
