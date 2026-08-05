package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// Um termo de busca costuma casar mais itens do que o mercado tem anunciado
// no momento: "Caixa de Armadura" existe em +5, +7, +8, +9, +11, +12 e +13 no
// servidor, e é normal que só uma ou duas dessas versões estejam à venda
// agora. Quem procura justamente a +13 via a tabela com a +7 e não tinha por
// onde acompanhar a que queria — a tela de histórico, que é onde as versões
// fora do mercado aparecem com um botão de watchlist, só é mostrada quando a
// busca não acha NADA (ver history.go).
//
// Esta é a saída para o caso do meio: a busca achou alguma coisa, e as outras
// versões ficam a um clique de distância. Sob demanda, e não junto da busca,
// porque descobri-las custa outra consulta ao site — e o normal é o usuário
// ter encontrado o que procurava na primeira tabela.

// variantsView é o fragmento com as versões do item que existem no servidor
// mas não estão anunciadas agora. Só um entre Error, Message e Rows aparece.
type variantsView struct {
	Error   string
	Message string
	Server  string
	Query   string
	Rows    []gnjoy.MarketPriceItem
}

// variantsURL monta o link que carrega as outras versões do item, informando
// quais itemIds a tabela de resultados já está mostrando — assim o fragmento
// nunca repete um item que está logo acima na tela, e descobrir isso não custa
// refazer a busca de mercado.
func variantsURL(server, item string, shown []gnjoy.ShopListItem) string {
	ids := make([]string, 0, len(shown))
	for _, it := range shown {
		id := strconv.Itoa(it.ItemId)
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	v := url.Values{}
	v.Set("server", server)
	v.Set("item", item)
	v.Set("shown", strings.Join(ids, ","))
	return "/web/search/variants?" + v.Encode()
}

// Variants trata GET /web/search/variants e devolve as versões do item que já
// foram vendidas no servidor mas não estão anunciadas agora, cada uma com o
// botão que a coloca na watchlist em modo de disponibilidade — o mesmo da
// tabela de histórico.
func (h *Handler) Variants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := strings.TrimSpace(q.Get("server"))
	item := strings.TrimSpace(q.Get("item"))
	if server == "" || item == "" {
		render(w, "variants.html.tmpl", variantsView{Error: "Informe o servidor e o nome do item."})
		return
	}

	shown := map[int]bool{}
	for _, raw := range strings.Split(q.Get("shown"), ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			shown[id] = true
		}
	}

	view := variantsView{Server: server, Query: item}

	// Só o histórico completo: a pergunta aqui é "que outras versões existem
	// neste servidor?", e um item que não vende há meses continua sendo uma
	// resposta válida — foi para poder acompanhá-lo que o usuário clicou.
	result, err := h.searchMarketPrice(r.Context(), server, item, gnjoy.MarketPricePeriodAll)
	if err != nil {
		view.Error = "Não foi possível consultar as outras versões agora. Tente novamente em instantes."
		render(w, "variants.html.tmpl", view)
		return
	}

	for _, row := range result.Items {
		if shown[row.ItemId] {
			continue
		}
		view.Rows = append(view.Rows, row)
	}
	if len(view.Rows) == 0 {
		view.Message = "Nenhuma outra versão deste item foi vendida no servidor " + server + "."
	}
	slog.Debug("web: versões fora do mercado consultadas",
		"item", item, "servidor", server, "encontradas", len(view.Rows))
	render(w, "variants.html.tmpl", view)
}
