package gnjoy

import (
	"regexp"
	"strconv"
)

// refinePrefixRe casa o nível de refino no início de um ItemFullName, ex.:
// "+11Laço da Celine[1]" -> "11". Itens em +0 não têm prefixo nenhum
// (apenas "Laço da Celine[1]").
var refinePrefixRe = regexp.MustCompile(`^\+(\d+)`)

// parseRefine extrai o nível de refino do nome completo do item retornado
// pelo detalhe da loja. Essa é a ÚNICA rota que expõe o refino: nem a busca
// (ShopListItem.ItemName) nem o detalhe do item (ItemDetail.ItemName)
// trazem essa informação — o site só mostra o refino embutido nesse prefixo
// "+N" do nome completo, o que faz anúncios do "mesmo" item aparecerem com
// preços muito diferentes na busca sem nenhuma explicação aparente.
func parseRefine(itemFullName string) int {
	m := refinePrefixRe.FindStringSubmatch(itemFullName)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}
