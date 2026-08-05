package gnjoy

import (
	"strings"
	"unicode"
)

// O backend de busca do GnJoy LATAM só aceita LETRAS, DÍGITOS E ESPAÇOS no
// termo procurado. Para QUALQUER searchWord que traga outro caractere, as duas
// páginas de busca — a de comércio e a de preços de mercado — respondem 200
// com o componente de erro do próprio site ("Tente novamente mais tarde.") no
// lugar da lista: não há list/totalCount na resposta, e o client não tem o que
// interpretar. É determinístico, e percent-encodar o caractere não muda nada
// (o backend decodifica antes de processar).
//
// Confirmado contra o site real, um caractere por requisição (ver
// docs/webtools-api-research.md): "-", "+", "(", ")", "[", "]", ".", ",",
// "'", "&", "!", "%" e "_" são todos recusados, enquanto acentos e dígitos
// passam sem problema. A regra é uma lista de PERMITIDOS, e não de proibidos:
// qualquer pontuação nova que apareça em um nome de item já entra contornada,
// em vez de virar mais um relato de "esse item some da watchlist".
//
// Isso não é um detalhe acadêmico — vários itens do jogo dependem disso:
//
//   - hífen: "Módulo de S-Rapidez", "Carta Peixe-Espada";
//   - "+": as caixas de refino têm o nível embutido no nome do item de
//     catálogo ("Caixa de Arma +13");
//   - parênteses: as pedras de encantamento trazem a posição no nome
//     ("Pedra de Mestre II (Baixo)");
//   - colchetes: os visuais ("[Visual] Chapéu Confeitado").
//
// Sem contorno, esses itens são impossíveis de buscar — e, pior, impossíveis
// de acompanhar na watchlist, que consulta o mercado pelo nome canônico do
// item ao buscar o preço ao vivo (ver internal/web/watchlist.go).
//
// O contorno se apoia no fato de a busca do site casar por TRECHO do nome:
// manda-se o maior pedaço só com caracteres aceitos e filtra-se a resposta
// aqui, pelo termo inteiro. O resultado é o mesmo que o site devolveria se
// aceitasse o caractere; o custo é uma lista maior trafegada, já que o pedaço
// enviado casa com mais itens do que o termo completo.

// isSearchable diz se r pode ir no searchWord enviado ao upstream.
func isSearchable(r rune) bool {
	return r == ' ' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// splitSearchWord decide o que enviar ao upstream no lugar de word e o que
// usar para filtrar a resposta. filter vem vazio quando o termo pode ir como
// está — o caso comum, em que a resposta do site já é exatamente a esperada.
//
// send vem vazio (com filter preenchido) quando o termo não tem nenhum trecho
// aceitável: não sobra pedaço nenhum para procurar. Cabe a quem chama tratar
// esse caso sem consultar o upstream, já que enviar um termo vazio devolveria
// o mercado inteiro.
func splitSearchWord(word string) (send, filter string) {
	rejected := func(r rune) bool { return !isSearchable(r) }
	if strings.IndexFunc(word, rejected) < 0 {
		return word, ""
	}
	for _, part := range strings.FieldsFunc(word, rejected) {
		part = strings.TrimSpace(part)
		if len(part) > len(send) {
			send = part
		}
	}
	return send, word
}

// matchesSearchWord reproduz o casamento por trecho do nome que o site faz,
// para filtrar a resposta de uma busca contornada por splitSearchWord.
//
// names é o nome do item em todas as formas pelas quais ele pode ser
// procurado: o nome cru devolvido pela busca e, quando o item tem cartas, o
// nome com o sufixo de slots ("Selo de Loki [1]"), que é como ele aparece na
// tela e é, portanto, o que o usuário digita.
func matchesSearchWord(word string, names ...string) bool {
	needle := strings.ToLower(word)
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), needle) {
			return true
		}
	}
	return false
}
