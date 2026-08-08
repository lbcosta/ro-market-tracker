package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// secoes conta as seções da tabela de resultados (uma por cabeçalho de grupo).
func secoes(html string) int {
	return strings.Count(html, `class="item-group-row"`)
}

var reChip = regexp.MustCompile(`<span class="(?:refine-badge|bonus-chip|group-chip[^"]*)">([^<]*)</span>`)

// chips devolve, na ordem, o texto de todas as etiquetas de cabeçalho de seção.
func chips(html string) []string {
	out := []string{}
	for _, m := range reChip.FindAllStringSubmatch(html, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// buscarComVarredura faz uma busca no NIDHOGG com os checkboxes ligados ou
// desligados e devolve o HTML da tabela.
func buscarComVarredura(t *testing.T, srv *httptest.Server, item string, refine, bonus bool) string {
	t.Helper()
	q := url.Values{"server": {"NIDHOGG"}, "item": {item}}
	if refine {
		q.Set("refine", "1")
	}
	if bonus {
		q.Set("bonus", "1")
	}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())
	return html
}

// TestBuscaSemCheckboxNaoConsultaDetalhes é a garantia central desta feature: o
// custo só existe para quem pediu. Sem isso, a busca de sempre passaria a valer
// uma consulta por anúncio — que é exatamente o que derrubou a primeira
// tentativa de busca por bônus.
func TestBuscaSemCheckboxNaoConsultaDetalhes(t *testing.T) {
	srv, mock := newWebServer(t)

	mock.ResetRequests()
	html := buscarComVarredura(t, srv, "Espada Primordial", false, false)

	if got := secoes(html); got != 1 {
		t.Errorf("seções = %d, quero 1: sem os checkboxes o agrupamento é só por item", got)
	}
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("busca custou %d requisições, quero 1 (só a própria busca)", got)
	}
	if len(chips(html)) != 0 {
		t.Errorf("nenhuma etiqueta deveria aparecer sem os checkboxes: %v", chips(html))
	}
}

// TestAgrupaPorRefino: os três anúncios da Espada Primordial são +0, +7 e +10, e
// custam de 129 a 299 milhões. Sem separar, a tabela mostra a diferença de preço
// sem mostrar o que a explica.
func TestAgrupaPorRefino(t *testing.T) {
	srv, mock := newWebServer(t)

	mock.ResetRequests()
	html := buscarComVarredura(t, srv, "Espada Primordial", true, false)

	if got := secoes(html); got != 3 {
		t.Errorf("seções = %d, quero 3 (uma por refino)\nHTML:\n%s", got, html)
	}
	// A +0 não ganha etiqueta: "+0" e "não refinável" são indistinguíveis no
	// dado que o site devolve, e anunciar "+0" seria afirmar o que não se sabe.
	if got := chips(html); len(got) != 2 || got[0] != "+7" || got[1] != "+10" {
		t.Errorf("etiquetas = %v, quero [+7 +10]", got)
	}
	// 1 busca + 1 detalhe de loja por anúncio.
	if got := mock.RequestCount(); got != 4 {
		t.Errorf("custou %d requisições, quero 4 (1 busca + 3 detalhes)", got)
	}
}

// TestAgrupaPorBonus: a +7 tem "CRIT +4" e "ATQ +3%", a +10 tem só "CRIT +4".
// Compartilhar um bônus não pode juntá-las — é a diferença entre "uma unidade
// com exatamente estes bônus" e "qualquer unidade com este bônus".
func TestAgrupaPorBonus(t *testing.T) {
	srv, mock := newWebServer(t)

	mock.ResetRequests()
	html := buscarComVarredura(t, srv, "Espada Primordial", false, true)

	if got := secoes(html); got != 3 {
		t.Errorf("seções = %d, quero 3 (uma por combinação de bônus)\nHTML:\n%s", got, html)
	}
	wantContains(t, html,
		`<span class="group-chip">sem bônus</span>`,
		`<span class="bonus-chip">CRIT &#43;4</span>`,
		`<span class="bonus-chip">ATQ &#43;3%</span>`,
	)
	if got := mock.RequestCount(); got != 4 {
		t.Errorf("custou %d requisições, quero 4 (1 busca + 3 detalhes de item)", got)
	}
}

// TestAgrupaPorRefinoEBonus: os dois juntos custam DUAS consultas por anúncio.
// É este número que o aviso da tela de busca traduz para o usuário.
func TestAgrupaPorRefinoEBonus(t *testing.T) {
	srv, mock := newWebServer(t)

	mock.ResetRequests()
	html := buscarComVarredura(t, srv, "Espada Primordial", true, true)

	if got := secoes(html); got != 3 {
		t.Errorf("seções = %d, quero 3", got)
	}
	if got := mock.RequestCount(); got != 7 {
		t.Errorf("custou %d requisições, quero 7 (1 busca + 3 lojas + 3 itens)", got)
	}
}

// TestVarreduraReusaOsMemos: refino e bônus nunca mudam para um mesmo ssi, então
// a segunda busca não pode voltar ao site por eles. É o que torna a feature
// utilizável mais de uma vez sem esgotar a cota.
func TestVarreduraReusaOsMemos(t *testing.T) {
	srv, mock := newWebServer(t)

	buscarComVarredura(t, srv, "Espada Primordial", true, true)

	mock.ResetRequests()
	// dir=desc para escapar do cache de busca (30s) e provar que o custo que
	// sobra é só o da busca em si.
	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada Primordial"}, "refine": {"1"}, "bonus": {"1"}, "dir": {"desc"}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	if got := secoes(html); got != 3 {
		t.Errorf("seções = %d, quero 3: o memo precisa produzir o mesmo agrupamento", got)
	}
	if got := mock.RequestCount(); got > 1 {
		t.Errorf("segunda busca custou %d requisições, quero no máximo 1: os detalhes já eram conhecidos", got)
	}
}

// TestExpandirReusaODetalheJaVerificadoPelaVarredura é a garantia de que a
// varredura e o card de detalhe não pagam duas vezes pelo mesmo anúncio: com
// os checkboxes ligados, GetStoreDetail e GetItemDetail de cada anúncio já
// foram consultados durante a busca — expandir a linha em seguida só falta
// buscar o histórico de preço, que nenhum outro caminho busca.
func TestExpandirReusaODetalheJaVerificadoPelaVarredura(t *testing.T) {
	srv, mock := newWebServer(t)

	buscarComVarredura(t, srv, "Espada Primordial", true, true)

	mock.ResetRequests()
	_, html := getHTML(t, srv, "/web/shops/303/835/s-primordial-158/expand")

	wantContains(t, html, `class="refine-badge">+7`, "CRIT &#43;4", "ATQ &#43;3%")
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("expandir custou %d requisições, quero 1 (só o histórico de preço): loja e item já tinham sido verificados pela varredura", got)
	}
}

// TestExpandirSemVarreduraContinuaCustandoAsTres garante que a memoização não
// vira um atalho: sem a varredura ter passado por ali, expandir uma linha
// pela primeira vez continua fazendo as três consultas de sempre.
func TestExpandirSemVarreduraContinuaCustandoAsTres(t *testing.T) {
	srv, mock := newWebServer(t)

	mock.ResetRequests()
	_, html := getHTML(t, srv, "/web/shops/303/835/s-primordial-158/expand")

	wantContains(t, html, `class="refine-badge">+7`)
	if got := mock.RequestCount(); got != 3 {
		t.Errorf("expandir custou %d requisições, quero 3 (loja, item, histórico)", got)
	}
}

// TestWatchlistReusaODetalheAoExpandir: o orçamento da watchlist com refino
// fixado também alimenta o memo compartilhado — expandir uma linha que a
// watchlist já consultou não deveria refazer a parte de loja/item.
func TestWatchlistReusaODetalheAoExpandir(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Espada Primordial"}, "itemId": {"600009"}, "refine": {"7"}}
	getHTML(t, srv, "/web/watchlist/price?"+q.Encode())

	mock.ResetRequests()
	_, html := getHTML(t, srv, "/web/shops/303/835/s-primordial-158/expand")

	wantContains(t, html, `class="refine-badge">+7`)
	if got := mock.RequestCount(); got != 2 {
		t.Errorf("expandir custou %d requisições, quero 2 (item + histórico: a watchlist só verifica refino, não bônus)", got)
	}
}

// TestVarreduraPulaOQueNuncaTemRefino: carta, consumível e material nunca têm
// refino nem bônus. Numa busca ampla isso é metade dos resultados, e cada
// consulta poupada é uma a menos contra o limite do site.
func TestVarreduraPulaOQueNuncaTemRefino(t *testing.T) {
	srv, mock := newWebServer(t)

	mock.ResetRequests()
	html := buscarComVarredura(t, srv, "Espada", true, true)

	wantContains(t, html, "Carta Peixe-Espada")

	// Só os anúncios de equipamento custam detalhe; se as cartas também
	// custassem, a conta subiria junto com elas.
	var equipamentos int
	for _, it := range gnjoytest.DemoConfig().Searches["Espada"].Items {
		if it.DatabaseType == "weapon" || it.DatabaseType == "armor" {
			equipamentos++
		}
	}
	want := 1 + 2*equipamentos
	if got := mock.RequestCount(); got != want {
		t.Errorf("custou %d requisições, quero %d (1 busca + 2 por equipamento, nada pelas cartas)", got, want)
	}
}

// TestVarreduraInterrompidaRenderizaParcial: a busca em si deu certo e já custou
// uma requisição — descartá-la e dizer "não foi possível buscar" seria mentira.
// O que não pode é o não verificado se misturar ao verificado: um anúncio de
// refino desconhecido dentro da seção "+0" é uma afirmação falsa sobre ele.
func TestVarreduraInterrompidaRenderizaParcial(t *testing.T) {
	srv, mock := newWebServer(t)

	// A busca passa; o primeiro detalhe falha e aborta o resto da varredura.
	mock.QueueFailure(gnjoytest.Failure{Passthrough: true}, 1)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 1)

	html := buscarComVarredura(t, srv, "Espada Primordial", true, false)

	wantContains(t, html,
		"results-table",
		"group-chip-desconhecido",
		"O site parou de responder no meio da verificação",
	)
	if strings.Contains(html, `class="error"`) {
		t.Errorf("a busca deu certo: o parcial é um aviso, não um erro\nHTML:\n%s", html)
	}
}

// TestSortURLPropagaOsCheckboxes: o link de ordenação substitui a tabela
// inteira. Sem carregar os checkboxes, clicar em "Preço" desagruparia a tabela
// com as caixas ainda marcadas na tela.
func TestSortURLPropagaOsCheckboxes(t *testing.T) {
	q := searchQuery{Server: "NIDHOGG", Item: "Espada", SortBy: "price", SortDir: "asc", ByRefine: true, ByBonus: true}

	got := q.sortURL("price")
	for _, want := range []string{"refine=1", "bonus=1", "sort=price", "dir=desc"} {
		if !strings.Contains(got, want) {
			t.Errorf("sortURL = %q, falta %q", got, want)
		}
	}

	// Sem os checkboxes eles não aparecem na URL: a busca de sempre continua
	// com a URL de sempre.
	limpa := searchQuery{Server: "NIDHOGG", Item: "Espada", SortBy: "price", SortDir: "asc"}
	if got := limpa.sortURL("qty"); strings.Contains(got, "refine") || strings.Contains(got, "bonus") {
		t.Errorf("sortURL = %q, não deveria trazer os checkboxes desligados", got)
	}
}

// TestBonusKeyIgnoraAOrdem: a mesma combinação pode chegar em ordens diferentes
// de anúncios diferentes; sem ordenar a chave, ela viraria duas seções.
func TestBonusKeyIgnoraAOrdem(t *testing.T) {
	a := bonusKey([]string{"CRIT +4", "ATQ +3%"})
	b := bonusKey([]string{"ATQ +3%", "CRIT +4"})
	if a != b {
		t.Errorf("bonusKey(%q) != bonusKey(%q): a ordem não pode importar", a, b)
	}
	if bonusKey(nil) == bonusKey([]string{"CRIT +4"}) {
		t.Error("sem bônus e com bônus precisam ser seções diferentes")
	}
}

// TestBotaoDaWatchlistLevaOsBonus: a combinação de bônus da seção viaja até a
// watchlist pelo data-bonus do botão. Sem isso, quem clicasse em "+ Watchlist"
// numa seção "CRIT +4 / ATQ +3%" ganharia uma linha seguindo o mais barato de
// qualquer bônus — outro item, outro preço.
func TestBotaoDaWatchlistLevaOsBonus(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarComVarredura(t, srv, "Espada Primordial", false, true)

	// As frases saem escapadas no atributo, como html/template faz com "+".
	for _, quero := range []string{
		`data-bonus="[&#34;CRIT &#43;4&#34;,&#34;ATQ &#43;3%&#34;]"`,
		`data-bonus="[&#34;CRIT &#43;4&#34;]"`,
	} {
		if !strings.Contains(html, quero) {
			t.Errorf("faltou %s no HTML da busca", quero)
		}
	}

	// A seção "sem bônus" fica de fora: um filtro que lista o que exigir não
	// tem como dizer "não quero bônus nenhum" (ver bonusJSON).
	if got := strings.Count(html, "data-bonus="); got != 2 {
		t.Errorf("há %d botões com data-bonus, quero 2 (a seção sem bônus não deve ter)", got)
	}
}

// TestBotaoDaWatchlistSemVarreduraNaoLevaBonus: sem o checkbox, a seção não
// representa uma combinação de bônus — ela junta todas as unidades do item.
// Mandar um filtro dali seria inventar um dado que ninguém verificou.
func TestBotaoDaWatchlistSemVarreduraNaoLevaBonus(t *testing.T) {
	srv, _ := newWebServer(t)

	html := buscarComVarredura(t, srv, "Espada Primordial", false, false)

	if strings.Contains(html, "data-bonus=") {
		t.Error("o botão trouxe data-bonus sem a varredura de bônus ter sido pedida")
	}
}
