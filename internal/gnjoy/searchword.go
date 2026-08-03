package gnjoy

import "strings"

// O backend de busca do GnJoy LATAM não aceita hífen no termo procurado.
//
// Para QUALQUER searchWord que contenha "-" (inclusive um "a-b"), as duas
// páginas de busca — a de comércio e a de preços de mercado — respondem 200
// com o componente de erro do próprio site ("Tente novamente mais tarde.") no
// lugar da lista: não há list/totalCount na resposta, e o client não tem o que
// interpretar. É determinístico, e percent-encodar o hífen não muda nada (o
// backend decodifica antes de processar).
//
// Isso não é um detalhe acadêmico: vários itens do jogo têm hífen no nome
// ("Módulo de S-Rapidez", "Carta Peixe-Espada"). Sem contorno, eles são
// impossíveis de buscar — e, pior, impossíveis de acompanhar na watchlist,
// que consulta o mercado pelo nome canônico do item.
//
// O contorno se apoia no fato de a busca do site casar por TRECHO do nome:
// manda-se o maior pedaço sem hífen do termo e filtra-se a resposta aqui,
// pelo termo inteiro. O resultado é o mesmo que o site devolveria se
// aceitasse o hífen; o custo é uma lista maior trafegada, já que o pedaço
// enviado casa com mais itens do que o termo completo.

// splitSearchWord decide o que enviar ao upstream no lugar de word e o que
// usar para filtrar a resposta. filter vem vazio quando o termo pode ir como
// está — o caso comum, em que a resposta do site já é exatamente a esperada.
//
// send vem vazio (com filter preenchido) quando o termo é feito só de hífens:
// não sobra pedaço nenhum para procurar. Cabe a quem chama tratar esse caso
// sem consultar o upstream, já que enviar um termo vazio devolveria o mercado
// inteiro.
func splitSearchWord(word string) (send, filter string) {
	if !strings.Contains(word, "-") {
		return word, ""
	}
	for _, part := range strings.Split(word, "-") {
		part = strings.TrimSpace(part)
		if len(part) > len(send) {
			send = part
		}
	}
	return send, word
}

// matchesSearchWord reproduz o casamento por trecho do nome que o site faz,
// para filtrar a resposta de uma busca contornada por splitSearchWord.
func matchesSearchWord(itemName, word string) bool {
	return strings.Contains(strings.ToLower(itemName), strings.ToLower(word))
}
