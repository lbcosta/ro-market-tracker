package web

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// A busca do GnJoy diz o preço de cada anúncio, mas não o que explica o preço:
// duas unidades do mesmo item podem custar 129 e 299 milhões porque uma é +10 e
// a outra +0, ou porque os bônus aleatórios de uma servem para a build de quem
// procura e os da outra não. Nenhuma das duas informações vem na busca — cada
// uma custa uma consulta de detalhe POR ANÚNCIO.
//
// Foi esse custo que derrubou a primeira tentativa de busca por bônus: uma
// busca de trinta anúncios virava trinta idas ao site, que respondia 429 e
// passava a recusar tudo, inclusive a watchlist. Por isso aqui a varredura é
// opcional (dois checkboxes desmarcados por padrão, com o custo escrito ao
// lado), memoizada por anúncio e abortada no primeiro erro.

// avisoDeVarreduraIncompleta explica na tabela o que ficou faltando nela. O
// banner de bloqueio, quando houver, explica o porquê; aqui interessa quantos
// anúncios continuam sem resposta, para ninguém ler a tabela como se ela
// estivesse completa.
func avisoDeVarreduraIncompleta(n int, byRefine, byBonus bool) string {
	oque := "o refino"
	switch {
	case byRefine && byBonus:
		oque = "o refino e os bônus"
	case byBonus:
		oque = "os bônus"
	}
	anuncios := "anúncios ficaram"
	if n == 1 {
		anuncios = "anúncio ficou"
	}
	return fmt.Sprintf("O site parou de responder no meio da verificação: %d %s sem %s confirmados.", n, anuncios, oque)
}

// itemFacts é o que a varredura descobriu sobre um anúncio. Os campos "Known"
// existem porque desconhecido NÃO é zero: Refine 0 é uma unidade sem refino, e
// juntar nele os anúncios que não chegaram a ser consultados colocaria uma +10
// de 300 milhões dentro da seção "+0".
type itemFacts struct {
	Refine      int
	RefineKnown bool
	Bonus       []string
	BonusKnown  bool
}

// bonusKey monta a parte da chave de agrupamento que representa os bônus de um
// anúncio. As frases são ordenadas porque a mesma combinação pode chegar em
// ordens diferentes de anúncios diferentes, e sem isso viraria duas seções.
// A ORDEM DE EXIBIÇÃO continua sendo a original (opt1..opt4, a do site) — só a
// chave é ordenada.
func bonusKey(bonus []string) string {
	ordenados := slices.Clone(bonus)
	sort.Strings(ordenados)
	return strings.Join(ordenados, "\x1f")
}

// lookupBonus devolve os bônus aleatórios de um anúncio — do memo
// compartilhado ou do upstream (ver Handler.itemDetail). Como o refino, eles
// nunca mudam para um mesmo ssi (a loja fechada e reaberta ganha um ssi
// novo), então cada anúncio custa no máximo UMA consulta na vida do processo,
// dividida com o card de detalhe caso a linha seja expandida depois.
func (h *Handler) lookupBonus(ctx context.Context, item gnjoy.ShopListItem) ([]string, error) {
	loc := gnjoy.StoreLocation{SvrId: item.SvrId, MapId: item.MapId, SSI: item.SSI}
	// NoRetry: isto roda dentro da varredura da busca, que aborta no primeiro
	// erro em vez de insistir (ver scanItemFacts).
	detail, err := h.itemDetail(ctx, loc, gnjoy.NoRetry())
	if err != nil {
		slog.Debug("web: não foi possível obter os bônus de um anúncio", "error", err)
		return nil, err
	}
	// RandomOptions monta uma fatia nova a cada chamada a partir dos
	// ponteiros já decodificados no ItemDetail — não há nada compartilhado
	// para clonar aqui, ao contrário de quando o memo guardava só a fatia.
	return detail.RandomOptions(), nil
}

// scanItemFacts descobre refino e/ou bônus de cada anúncio, conforme os
// checkboxes da busca, e devolve o que aprendeu junto da contagem de anúncios
// que ficaram sem resposta.
//
// Sem teto de requisições: com os dois checkboxes ligados são DUAS consultas
// por anúncio, e uma busca de trinta anúncios passa de um minuto no rate
// limiter. É por isso que o formulário mostra o custo em números antes de o
// usuário marcar a caixa.
//
// A varredura segue a ordem já ordenada da tabela, e não a de chegada: se
// parar no meio, os anúncios verificados são os mais baratos — os que
// interessam a quem está comparando preço.
func (h *Handler) scanItemFacts(ctx context.Context, items []gnjoy.ShopListItem, wantRefine, wantBonus bool) (map[string]itemFacts, int) {
	if !wantRefine && !wantBonus {
		return nil, 0
	}

	// Já suspenso antes de começar: nem a primeira consulta sai, então o
	// caminho todo vira "não verificado" sem gastar nada.
	if h.client.Suspension().Current().Suspended {
		return nil, len(items)
	}

	facts := make(map[string]itemFacts, len(items))
	naoVerificados := 0
	abortada := false

	for _, it := range items {
		if abortada {
			naoVerificados++
			continue
		}
		// O contexto morre quando o usuário dá F5 ou dispara outra busca: a
		// resposta desta não vai a lugar nenhum, e seguir varrendo só gastaria
		// a cota do site.
		if ctx.Err() != nil {
			abortada = true
			naoVerificados++
			continue
		}

		f := itemFacts{}
		// Carta, consumível e material nunca têm refino nem bônus — o site usa
		// um tipo só ("armor") para tudo que se veste. Consultar o detalhe
		// deles seria requisição jogada fora, e numa busca ampla como "Espada"
		// isso é metade dos resultados.
		equipamento := isEquipment(it.DatabaseType)

		if wantRefine {
			if !equipamento {
				f.RefineKnown = true
			} else if refine, err := h.fetchStoreRefine(ctx, it); err == nil {
				f.Refine, f.RefineKnown = refine, true
			} else {
				abortada = true
			}
		}
		if wantBonus && !abortada {
			if !equipamento {
				f.BonusKnown = true
			} else if bonus, err := h.lookupBonus(ctx, it); err == nil {
				f.Bonus, f.BonusKnown = bonus, true
			} else {
				abortada = true
			}
		}

		if abortada {
			// Um erro numa varredura de N anúncios é o site pedindo calma, não
			// azar com aquele anúncio: insistir nos N-1 seguintes é exatamente
			// o que transforma um tropeço em bloqueio. O que já foi descoberto
			// vale (e ficou memoizado); o resto vira "não verificado" na tela.
			slog.Warn("web: varredura de detalhes interrompida, o site parou de responder",
				"verificados", len(facts), "restantes", len(items)-len(facts))
			naoVerificados++
			continue
		}
		facts[storeKey(it)] = f
	}
	return facts, naoVerificados
}
